package env

import (
	"strings"
	"testing"
)

func reader(vars map[string]string) *Reader {
	return New(func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	})
}

func TestReaderCollectsEveryProblem(t *testing.T) {
	r := reader(map[string]string{
		"A_BOOL": "maybe", "A_INT": "12x", "A_RANGE": "500", "A_FLOAT": "-1", "A_ENUM": "x",
		"A_PATH": "relative/dir", "A_CIDRS": "10.0.0.0/8, nope", "A_SECRET": "short",
	})
	r.Bool("A_BOOL", false)
	r.Int("A_INT", 1, 0, 10)
	r.Int("A_RANGE", 1, 0, 10)
	r.Float("A_FLOAT", 0, 0, 1)
	r.Enum("A_ENUM", "a", "a", "b")
	r.AbsPath("A_PATH", "")
	r.Prefixes("A_CIDRS", "")
	r.Secret("A_SECRET", 32)
	r.Required("A_MISSING")
	ps, ok := AsProblems(r.Err())
	if !ok || len(ps) != 9 {
		t.Fatalf("problems = %v", r.Err())
	}
	if ps[0].Var != "A_BOOL" || !strings.Contains(r.Err().Error(), "9 invalid environment variable(s)") {
		t.Fatalf("sorted by variable: %v", ps)
	}
	if strings.Contains(r.Err().Error(), "short") || strings.Contains(r.Err().Error(), "relative/dir") {
		t.Fatal("values must never be written in the problems")
	}
}

func TestReaderValuesAndDefaults(t *testing.T) {
	r := reader(map[string]string{"B": " yes ", "I": "7", "L": " a, ,b ", "P": "/data/x/", "E": "  ", "C": "10.0.0.1/24,127.0.0.1"})
	if !r.Bool("B", false) || r.Int("I", 0, 0, 10) != 7 || r.String("E", "def") != "def" || r.AbsPath("P", "") != "/data/x" {
		t.Fatal("values")
	}
	if l := r.List("L", ""); len(l) != 2 || l[1] != "b" {
		t.Fatalf("list = %v", l)
	}
	if c := r.Prefixes("C", ""); len(c) != 2 || c[0].String() != "10.0.0.0/24" || c[1].Bits() != 32 {
		t.Fatalf("prefixes = %v", c)
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

func TestCheckUnknownAndRemoved(t *testing.T) {
	r := reader(map[string]string{"SERVICES_ENABLED": "true"})
	r.Bool("SERVICES_ENABLED", false)
	r.String("SERVICES_NETWORK", "")
	r.Declare("SERVICES_ENGINES")
	r.Removed("use the panel", "SERVICES_EXPOSED_PORTS")
	r.CheckUnknown([]string{
		"SERVICES_ENABLED=true", "SERVICES_NETWROK=x", "SERVICES_ENGINES=pg", "SERVICES_EXPOSED_PORTS=a=1",
		"SERVICES_TOTALLY_OTHER=1", "HOME=/root", "SERVICES_EXPOSED_PORTS_EMPTY=",
	}, "SERVICES_")
	msg := r.Err().Error()
	for _, want := range []string{
		"SERVICES_NETWROK: unknown variable (did you mean SERVICES_NETWORK?)",
		"SERVICES_EXPOSED_PORTS: no longer supported: use the panel",
		"SERVICES_TOTALLY_OTHER: unknown variable",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "HOME") {
		t.Fatal("variables without a known prefix are ignored")
	}
	// A removed variable set to an empty value is harmless.
	r = reader(nil)
	r.Removed("x", "OLD_VAR")
	r.CheckUnknown([]string{"OLD_VAR="}, "OLD_")
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

func TestDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{{"", "abc", 3}, {"kitten", "sitting", 3}, {"NETWROK", "NETWORK", 2}, {"same", "same", 0}} {
		if got := distance(c.a, c.b); got != c.d {
			t.Errorf("distance(%q,%q) = %d", c.a, c.b, got)
		}
	}
}
