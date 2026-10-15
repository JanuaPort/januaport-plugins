//go:build windows

// Spike JanuaPort/januaport#1044 P0, Sonde jobobject-kill: Job Object mit
// KILL_ON_JOB_CLOSE, Prozess- und Speicherdeckel um ein Embeddable-Python.
// Kein Produktcode.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const stillActive = 259

func probe(name string, ok bool, format string, a ...any) {
	status := "FAIL"
	if ok {
		status = "PASS"
	}
	detail := strings.Join(strings.Fields(fmt.Sprintf(format, a...)), " ")
	fmt.Printf("PROBE %s %s %s\n", name, status, detail)
}

func newJob(flags uint32, procLimit uint32, memLimit uintptr) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = flags
	info.BasicLimitInformation.ActiveProcessLimit = procLimit
	info.JobMemoryLimit = memLimit
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return job, nil
}

// startInJob startet Python erst nach dem Zuweisen zum Job los: das Kind wartet
// auf eine Zeile auf stdin. Im Produkt ersetzt CREATE_SUSPENDED dieses Muster.
func startInJob(job windows.Handle, python string, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(python, append([]string{"-I"}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Start: %w", err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := io.WriteString(stdin, "go\n"); err != nil {
		return nil, err
	}
	return cmd, nil
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // existiert nicht mehr
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

func waitFile(path string, d time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, false
}

func testKill(python, scripts, work string) {
	const name = "jobobject-kill"
	out := filepath.Join(work, "kill.json")
	_ = os.Remove(out)
	job, err := newJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, 0, 0)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	cmd, err := startInJob(job, python, filepath.Join(scripts, "child.py"), out)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	raw, ok := waitFile(out, 30*time.Second)
	if !ok {
		_ = windows.CloseHandle(job)
		probe(name, false, "Kind meldete keine PIDs binnen 30 s")
		return
	}
	var pids map[string]int
	if err := json.Unmarshal(raw, &pids); err != nil {
		_ = windows.CloseHandle(job)
		probe(name, false, "PID-Datei unlesbar: %v (%s)", err, raw)
		return
	}
	before := map[string]bool{}
	for role, pid := range pids {
		before[role] = alive(pid)
	}
	_ = windows.CloseHandle(job) // der Kern der Sonde: Handle schliessen
	time.Sleep(3 * time.Second)
	_ = cmd.Wait()
	var survivors []string
	for role, pid := range pids {
		if alive(pid) {
			survivors = append(survivors, fmt.Sprintf("%s=%d", role, pid))
		}
	}
	probe(name, len(survivors) == 0, "vorher lebend %v, Ueberlebende nach CloseHandle: %v (PIDs %v)", before, survivors, pids)
}

func testProcCap(python, scripts, work string) {
	const name = "jobobject-proc-cap"
	out := filepath.Join(work, "cap.json")
	_ = os.Remove(out)
	job, err := newJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE|windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS, 3, 0)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	defer windows.CloseHandle(job)
	cmd, err := startInJob(job, python, filepath.Join(scripts, "cap.py"), out)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	defer func() { _ = cmd.Process.Kill() }()
	raw, ok := waitFile(out, 40*time.Second)
	if !ok {
		probe(name, false, "cap.py meldete nichts binnen 40 s")
		return
	}
	var res struct {
		Limit    int      `json:"limit"`
		Spawns   []string `json:"spawns"`
		Survived int      `json:"survived"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		probe(name, false, "Ergebnis unlesbar: %v (%s)", err, raw)
		return
	}
	// Limit 3 = Python selbst + 2 weitere; ab dem n+1-ten Prozess darf nichts mehr leben.
	probe(name, res.Survived == res.Limit-1, "ActiveProcessLimit=%d, ueberlebende Kinder=%d, Starts=%v", res.Limit, res.Survived, res.Spawns)
}

func testMemCap(python, scripts, work string) {
	const name = "jobobject-mem-cap"
	const limitMB = 200
	out := filepath.Join(work, "mem.json")
	_ = os.Remove(out)
	job, err := newJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE|windows.JOB_OBJECT_LIMIT_JOB_MEMORY, 0, limitMB<<20)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	defer windows.CloseHandle(job)
	cmd, err := startInJob(job, python, filepath.Join(scripts, "mem.py"), out)
	if err != nil {
		probe(name, false, "%v", err)
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		probe(name, false, "mem.py lief 60 s ohne Ende")
		return
	}
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	raw, _ := os.ReadFile(out)
	var res struct {
		ReachedMB int    `json:"reached_mb"`
		Outcome   string `json:"outcome"`
	}
	_ = json.Unmarshal(raw, &res)
	// Ziel waren 1000 MB; unter dem Deckel (plus Interpreter-Overhead) muss Schluss sein.
	ok := res.ReachedMB > 0 && res.ReachedMB < 1000
	probe(name, ok, "JobMemoryLimit=%d MB, erreicht=%d MB, Ausgang=%q, Exitcode=0x%x", limitMB, res.ReachedMB, res.Outcome, uint32(exit))
}

func main() {
	python := flag.String("python", "", "Pfad zu python.exe (Embeddable)")
	scripts := flag.String("scripts", "", "Ordner mit child.py, cap.py, mem.py")
	work := flag.String("work", "", "Arbeitsordner fuer Ergebnisdateien")
	flag.Parse()
	if *python == "" || *scripts == "" || *work == "" {
		fmt.Fprintln(os.Stderr, "usage: jobprobe -python P -scripts D -work W")
		os.Exit(2)
	}
	testKill(*python, *scripts, *work)
	testProcCap(*python, *scripts, *work)
	testMemCap(*python, *scripts, *work)
}
