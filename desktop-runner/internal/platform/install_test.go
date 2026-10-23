package platform

import (
	"errors"
	"testing"
)

type fakeAutostart struct {
	err          error
	policy       bool
	run, removed bool
}

func (f *fakeAutostart) Register(string) error   { return f.err }
func (f *fakeAutostart) RemoveTask() error       { f.removed = true; return nil }
func (f *fakeAutostart) SetRun(string) error     { f.run = true; return nil }
func (f *fakeAutostart) RemoveRun() error        { f.run = false; return nil }
func (f *fakeAutostart) PolicyDenied(error) bool { return f.policy }
func TestAutostartRueckfallNurBeiRichtlinie(t *testing.T) {
	for _, v := range []struct {
		err          error
		policy, want bool
	}{{nil, false, false}, {errors.New("denied"), true, true}, {errors.New("other"), false, false}} {
		f := &fakeAutostart{err: v.err, policy: v.policy}
		fallback, e := InstallWith(f, "desktop-runner.exe")
		if fallback != v.want || f.run != v.want {
			t.Fatal(v)
		}
		if v.err != nil && !v.policy && e == nil {
			t.Fatal("anderer Fehler verschluckt")
		}
		if e = UninstallWith(f); e != nil || f.run || !f.removed {
			t.Fatal(e)
		}
	}
}
