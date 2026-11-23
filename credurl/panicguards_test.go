package credurl

import "testing"

// held runs f and turns a panic into a test failure that names the guard, so a
// removed guard fails by ASSERTION rather than by a panic that ends the binary
// (which mutsweep reads as luck, not proof).
func held(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panicked instead of answering: %v", name, r)
		}
	}()
	f()
}

// credurl.go:278: no @ before the host, or a path before it, is no scp remote.
func TestAWordWithNoAtIsNoRemote(t *testing.T) {
	for _, s := range []string{"origin", "@host:path", "a/b@host:path", "a b@host:path", ""} {
		held(t, "Inspect "+s, func() {
			if f := Inspect(s); f != (Finding{}) {
				t.Errorf("Inspect(%q) = %+v, want the zero Finding", s, f)
			}
		})
	}
}

// credurl.go:283: an scp-style remote needs a colon after the host.
func TestAnScpRemoteWithoutAColonIsNoRemote(t *testing.T) {
	for _, s := range []string{"git@host", "git@host/path", "git@:path"} {
		held(t, "Inspect "+s, func() {
			if f := Inspect(s); f != (Finding{}) {
				t.Errorf("Inspect(%q) = %+v, want the zero Finding", s, f)
			}
		})
	}
}

// credurl.go:308: an authority with no @ has a host and nothing else.
func TestAnAuthorityWithNoAtHasOnlyAHost(t *testing.T) {
	held(t, "Inspect https://Host:8080/o/r", func() {
		f := Inspect("https://Host:8080/o/r")
		if f != (Finding{Host: "host"}) {
			t.Errorf("Inspect = %+v, want only Host \"host\"", f)
		}
	})
}
