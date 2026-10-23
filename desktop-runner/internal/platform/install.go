package platform

// Autostart is the seam used by Linux CI; the Windows implementation uses COM
// and HKCU directly. Fallback is limited to access/policy denial.
type Autostart interface {
	Register(string) error
	RemoveTask() error
	SetRun(string) error
	RemoveRun() error
	PolicyDenied(error) bool
}

func InstallWith(a Autostart, exe string) (bool, error) {
	if err := a.Register(exe); err != nil {
		if !a.PolicyDenied(err) {
			return false, err
		}
		return true, a.SetRun(exe)
	}
	return false, a.RemoveRun()
}
func UninstallWith(a Autostart) error {
	first := a.RemoveTask()
	second := a.RemoveRun()
	if first != nil {
		return first
	}
	return second
}
