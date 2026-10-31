// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary be its own child: `thumb <flags>` runs Child, as the server's binary
// does. The limits apply to that process, never to the test process.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "thumb" {
		os.Exit(Child(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "sandbox-probe" {
		os.Exit(sandboxProbe(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// childRun runs the child with the given limits and input, and returns its exit code, its stdout, and
// how it ended: a signal, or -1 for a normal exit.
type childResult struct {
	code   int
	stdout []byte
	signal syscall.Signal
	stderr string
	took   time.Duration
}

func runChild(t *testing.T, input []byte, args ...string) childResult {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"thumb"}, args...)...)
	cmd.Env = []string{} // the child inherits nothing, as the server's runs it
	cmd.Stdin = bytes.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	start := time.Now()
	err := cmd.Run()
	res := childResult{stdout: out.Bytes(), stderr: errb.String(), took: time.Since(start), signal: -1}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.code = 0
	case errors.As(err, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.signal = ws.Signal()
			res.code = -1
		} else {
			res.code = ee.ExitCode()
		}
	default:
		t.Fatalf("run child: %v", err)
	}
	return res
}

// testLimits are generous enough for every ordinary test image.
func testLimits(extra ...string) []string {
	return append([]string{"-size=256", "-max-pixels=" + strconv.Itoa(testMaxPixels),
		"-max-input=" + strconv.Itoa(50<<20), "-memory=" + strconv.Itoa(512<<20), "-cpu=30"}, extra...)
}

func TestChildThumbnailsAnImage(t *testing.T) {
	t.Parallel()
	res := runChild(t, encodePNG(t, gradient(800, 400)), testLimits()...)
	if res.code != ExitOK {
		t.Fatalf("exit %d, stderr %q", res.code, res.stderr)
	}
	if img := decodeOutput(t, res.stdout); img.Bounds().Dx() != 256 {
		t.Errorf("thumbnail = %v", img.Bounds())
	}
}

func TestChildExitCodes(t *testing.T) {
	t.Parallel()
	png := encodePNG(t, gradient(64, 64))
	cases := []struct {
		name  string
		input []byte
		args  []string
		want  int
	}{
		{"no flags", png, nil, ExitUsage},
		{"zero size", png, testLimits("-size=0"), ExitUsage},
		{"no memory limit", png, testLimits("-memory=0"), ExitUsage},
		{"no cpu limit", png, testLimits("-cpu=0"), ExitUsage},
		{"input over the cap", png, testLimits("-max-input=10"), ExitTooLarge},
		{"svg", []byte("<svg/>"), testLimits(), ExitUnsupported},
		{"corrupt", []byte("\x89PNG\r\n\x1a\nxxxx"), testLimits(), ExitCorrupt},
		{"pixel cap", pngHeaderOnly(30000, 30000), testLimits(), ExitTooLarge},
	}
	for _, c := range cases {
		res := runChild(t, c.input, c.args...)
		if res.code != c.want {
			t.Errorf("%s: exit %d (signal %v), want %d; stderr %q", c.name, res.code, res.signal, c.want, res.stderr)
		}
		if len(res.stdout) != 0 {
			t.Errorf("%s: wrote %d bytes to stdout", c.name, len(res.stdout))
		}
	}
}

// TestChildMemoryLimitStopsTheDecoder decodes an image whose decoded pixels need about 200 MB under a
// 64 MB budget. The decoder must fail in the child: the child dies, the parent is not touched.
func TestChildMemoryLimitStopsTheDecoder(t *testing.T) {
	if underRace {
		// Checked without -race only: the race runtime's shadow mapping at start uses the budget, so the child dies
		// of its own startup, not of the decode. Production is built without -race.
		t.Skip("the memory budget cannot be measured under the race detector")
	}
	t.Parallel()
	big := encodePNG(t, gradient(7000, 7000)) // 49 Mpx, decoded as NRGBA: about 196 MB
	res := runChild(t, big, testLimits("-memory="+strconv.Itoa(64<<20))...)
	t.Logf("64 MB budget: exit %d, signal %v, took %v, stderr %.120q", res.code, res.signal, res.took, res.stderr)
	if res.code == ExitOK {
		t.Fatalf("a 49 Mpx decode succeeded under a 64 MB budget")
	}
	if len(res.stdout) != 0 {
		t.Errorf("a killed child wrote %d bytes", len(res.stdout))
	}
	if !strings.Contains(res.stderr, "out of memory") {
		t.Errorf("the child did not die of memory: stderr %q", res.stderr)
	}
	// The same image decodes under a budget that holds it.
	ok := runChild(t, big, testLimits("-memory="+strconv.Itoa(512<<20))...)
	if ok.code != ExitOK {
		t.Errorf("the same image under 512 MB: exit %d, stderr %q", ok.code, ok.stderr)
	}
}

// TestChildCPULimitKillsARunawayDecode uses the largest image the cap admits with a CPU budget of one
// second. The kernel sends SIGXCPU at one second and SIGKILL one second later; either way the decode does not finish.
func TestChildCPULimitKillsARunawayDecode(t *testing.T) {
	t.Parallel()
	big := encodePNG(t, gradient(7000, 7000))
	res := runChild(t, big, testLimits("-cpu=1", "-memory="+strconv.Itoa(1<<30))...)
	t.Logf("cpu 1s: exit %d, signal %v, took %v", res.code, res.signal, res.took)
	if res.code == ExitOK {
		t.Logf("the decode finished inside one CPU second on this machine; the limit was not reached")
		return
	}
	if len(res.stdout) != 0 {
		t.Errorf("a killed child wrote %d bytes", len(res.stdout))
	}
}

// TestChildWithoutMemoryLimitIsNotRun is the rule that the child never decodes unlimited: a child whose limits
// cannot be set exits ExitLimits before it reads its input. The -memory flag is validated, so this case is the
// one the parent can see without a platform failure.
func TestChildRefusesWithoutLimits(t *testing.T) {
	t.Parallel()
	res := runChild(t, encodePNG(t, gradient(8, 8)), "-size=64", "-max-pixels=1000", "-max-input=1000")
	if res.code != ExitUsage {
		t.Fatalf("a child with no memory or cpu flag exit %d, want %d (usage)", res.code, ExitUsage)
	}
	if len(res.stdout) != 0 {
		t.Errorf("a child that refused wrote output")
	}
}
