// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// sandboxProbe is the child of TestSandboxDeniesFilesAndNetwork. Its arguments are a data file, a TCP address and
// "on" or "off". "on" runs harden, the same code Child runs before it reads its input. The probe then tries to open
// the data file and /etc/passwd, and to connect to the address, and prints what each attempt returned.
func sandboxProbe(args []string) int {
	if len(args) != 3 {
		return 2
	}
	if args[2] == "on" {
		lim := Limits{Size: 256, MaxPixels: testMaxPixels, MaxInput: 1 << 20, MemoryBytes: 512 << 20, CPUSeconds: 30}
		if err := harden(lim); err != nil {
			fmt.Println("harden:", err)
			return 3
		}
	}
	_, dataErr := os.Open(args[0])
	_, passwdErr := os.Open("/etc/passwd")
	conn, dialErr := net.Dial("tcp", args[1])
	if conn != nil {
		conn.Close()
	}
	var nofile, fsize, core syscall.Rlimit
	_ = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &nofile)
	_ = syscall.Getrlimit(syscall.RLIMIT_FSIZE, &fsize)
	_ = syscall.Getrlimit(syscall.RLIMIT_CORE, &core)
	fmt.Printf("data=%v passwd=%v connect=%v limits=nofile:%d,fsize:%d,core:%d\n", dataErr, passwdErr, dialErr,
		nofile.Cur, fsize.Cur, core.Cur)
	return 0
}

// runProbe runs the probe in a child process with an empty environment, as the server runs the decoder.
func runProbe(t *testing.T, data, addr, mode string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "sandbox-probe", data, addr, mode)
	cmd.Env = []string{}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("probe %s: %v, stderr %q, stdout %q", mode, err, errb.String(), out.String())
	}
	return strings.TrimSpace(out.String())
}

// TestSandboxDeniesFilesAndNetwork: on this kernel the decoder child cannot open the data file or /etc/passwd, and cannot
// connect, once harden has run. Without the sandbox the same probe does all three, so the denial is the sandbox's.
// The network half needs Landlock ABI 4; a kernel without it skips that half and says so.
func TestSandboxDeniesFilesAndNetwork(t *testing.T) {
	abi := LandlockABI()
	if abi == 0 {
		t.Skip("this kernel has no Landlock: nothing to test")
	}
	if abi < 4 {
		t.Skipf("this kernel's Landlock is ABI %d; the network rights need ABI 4", abi)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	data := filepath.Join(t.TempDir(), "data.db")
	if err := os.WriteFile(data, []byte("the data directory's file"), 0o600); err != nil {
		t.Fatal(err)
	}
	off := runProbe(t, data, ln.Addr().String(), "off")
	if !strings.HasPrefix(off, "data=<nil> passwd=<nil> connect=<nil>") {
		t.Fatalf("without the sandbox the probe was refused: %q", off)
	}
	on := runProbe(t, data, ln.Addr().String(), "on")
	if n := strings.Count(on, "permission denied"); n != 3 {
		t.Fatalf("with the sandbox %d of 3 attempts were refused: %q", n, on)
	}
	// The same harden applies the descriptor, file-size and core limits, at their hard values.
	if !strings.HasSuffix(on, "limits=nofile:16,fsize:0,core:0") {
		t.Errorf("limits of the sandboxed child: %q; want nofile 16, fsize 0, core 0", on)
	}
}
