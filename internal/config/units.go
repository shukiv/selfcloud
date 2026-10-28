// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration written as "30s", "24h" and so on.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// ByteSize is a number of bytes written as "1048576", "64MiB", "95MB", "1.5GiB" and so on.
// KB, MB, GB and TB are powers of 1000; KiB, MiB, GiB and TiB are powers of 1024.
type ByteSize int64

// Binary and decimal byte units.
const (
	KiB ByteSize = 1 << (10 * (iota + 1))
	MiB
	GiB
	TiB
)

var byteUnits = map[string]float64{
	"":    1,
	"b":   1,
	"kb":  1e3,
	"mb":  1e6,
	"gb":  1e9,
	"tb":  1e12,
	"kib": float64(KiB),
	"mib": float64(MiB),
	"gib": float64(GiB),
	"tib": float64(TiB),
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	num, unit := s, ""
	if i >= 0 {
		num, unit = s[:i], strings.ToLower(strings.TrimSpace(s[i:]))
	}
	mult, ok := byteUnits[unit]
	if !ok || num == "" {
		return fmt.Errorf("invalid byte size %q", s)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 {
		return fmt.Errorf("invalid byte size %q", s)
	}
	v := math.Round(f * mult)
	if v >= math.MaxInt64 {
		return fmt.Errorf("byte size %q is too large", s)
	}
	*b = ByteSize(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (b ByteSize) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

func (b ByteSize) String() string {
	for _, u := range []struct {
		size ByteSize
		name string
	}{{TiB, "TiB"}, {GiB, "GiB"}, {MiB, "MiB"}, {KiB, "KiB"}} {
		if b >= u.size && b%u.size == 0 {
			return strconv.FormatInt(int64(b/u.size), 10) + u.name
		}
	}
	return strconv.FormatInt(int64(b), 10)
}
