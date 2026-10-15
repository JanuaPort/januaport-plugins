//go:build windows

// Spike JanuaPort/januaport#1044 P0 Teil 2, Sonde jobobject-real-app: startet eine echte Anwendung in einem
// Job Object (KILL_ON_JOB_CLOSE), schliesst danach das Handle und meldet, welche neu entstandenen Prozesse
// ueberleben. Variante der jobprobe aus Teil 1, hier gegen klassische und Paket-Apps (MSIX).
// Muss in der interaktiven Benutzersitzung laufen (GUI-Anwendungen). Kein Produktcode.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// outFile: Ergebnisse gehen an die Datei, damit das Programm ohne Konsole (-H=windowsgui) laufen kann. Mit Konsole
// haelt Windows Terminal als Standardterminal die Konsole und verfaelscht die Messung gegen Windows Terminal.
var outFile string

func emit(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	if outFile == "" {
		fmt.Print(line)
		return
	}
	if f, err := os.OpenFile(outFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
}

type proc struct {
	pid, ppid uint32
	name      string
}

// Hintergrundprozesse, die waehrend der Messung unabhaengig von der Anwendung entstehen koennen.
var noise = map[string]bool{
	"svchost.exe": true, "conhost.exe": true, "runtimebroker.exe": true, "wmiprvse.exe": true, "dllhost.exe": true,
	"backgroundtaskhost.exe": true, "taskhostw.exe": true, "searchhost.exe": true, "textinputhost.exe": true,
	"smartscreen.exe": true, "mpcmdrun.exe": true, "msmpeng.exe": true, "audiodg.exe": true, "ctfmon.exe": true,
	"sihost.exe": true, "shellexperiencehost.exe": true, "startmenuexperiencehost.exe": true, "tiworker.exe": true,
	"wsmprovhost.exe": true, "pythonw.exe": true, "python.exe": true, "appjob.exe": true,
	"applicationframehost.exe": true, "searchindexer.exe": true, "wermgr.exe": true, "werfault.exe": true,
}

func snapshot() (map[uint32]proc, error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	out := map[uint32]proc{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
		out[e.ProcessID] = proc{e.ProcessID, e.ParentProcessID, windows.UTF16ToString(e.ExeFile[:])}
	}
	return out, nil
}

func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return job, err
}

// startSuspendedInJob startet den Prozess angehalten, weist ihn dem Job zu und setzt ihn dann fort.
func startSuspendedInJob(job windows.Handle, exe, args string) (uint32, error) {
	cmdline := `"` + exe + `"`
	if args != "" {
		cmdline += " " + args
	}
	cl, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return 0, err
	}
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cl, nil, nil, false, windows.CREATE_SUSPENDED, nil, nil, &si, &pi); err != nil {
		return 0, fmt.Errorf("CreateProcess: %w", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return 0, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		return 0, fmt.Errorf("ResumeThread: %w", err)
	}
	return pi.ProcessId, nil
}

func inJob(pid uint32, job windows.Handle) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "?"
	}
	defer windows.CloseHandle(h)
	var res int32
	r, _, _ := procIsProcessInJob.Call(uintptr(h), uintptr(job), uintptr(unsafe.Pointer(&res)))
	if r == 0 {
		return "?"
	}
	if res != 0 {
		return "ja"
	}
	return "NEIN"
}

func kill(pid uint32) {
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	}
}

func main() {
	label := flag.String("label", "", "Name der Sonde, z. B. notepad-klassisch")
	exe := flag.String("exe", "", "Pfad oder Alias der Anwendung")
	args := flag.String("args", "", "Argumente als eine Zeichenkette")
	wait := flag.Int("wait", 10, "Sekunden Wartezeit, bis die Anwendung ihre Prozesse gestartet hat")
	out := flag.String("out", "", "Ergebnisdatei (angehaengt); noetig, weil das Programm ohne Konsole gebaut wird")
	flag.Parse()
	outFile = *out
	if *label == "" || *exe == "" {
		fmt.Fprintln(os.Stderr, "usage: appjob -label L -exe E [-args A] [-wait S]")
		os.Exit(2)
	}
	name := "jobobject-" + *label
	fail := func(f string, a ...any) {
		emit("PROBE %s FAIL %s\n", name, fmt.Sprintf(f, a...))
		os.Exit(1)
	}

	before, err := snapshot()
	if err != nil {
		fail("Snapshot: %v", err)
	}
	job, err := newJob()
	if err != nil {
		fail("Job: %v", err)
	}
	rootPid, err := startSuspendedInJob(job, *exe, *args)
	if err != nil {
		fail("%v", err)
	}
	time.Sleep(time.Duration(*wait) * time.Second)

	during, _ := snapshot()
	var created []proc
	for pid, p := range during {
		if _, ok := before[pid]; !ok && pid != uint32(os.Getpid()) {
			created = append(created, p)
		}
	}
	sort.Slice(created, func(i, j int) bool { return created[i].pid < created[j].pid })
	var seen []string
	for _, p := range created {
		seen = append(seen, fmt.Sprintf("%s(%d,ppid=%d,imJob=%s)", p.name, p.pid, p.ppid, inJob(p.pid, job)))
	}

	_ = windows.CloseHandle(job) // Kern der Sonde
	time.Sleep(4 * time.Second)
	after, _ := snapshot()
	var survivors, ignored []string
	for _, p := range created {
		if a, ok := after[p.pid]; ok && a.name == p.name {
			entry := fmt.Sprintf("%s(%d)", p.name, p.pid)
			if noise[strings.ToLower(p.name)] {
				ignored = append(ignored, entry)
				continue
			}
			survivors = append(survivors, entry)
		}
	}
	for _, p := range created { // aufraeumen, damit die naechste Sonde sauber startet
		if _, ok := after[p.pid]; ok {
			kill(p.pid)
		}
	}
	status := "PASS"
	if len(survivors) > 0 {
		status = "FAIL"
	}
	emit("PROBE %s %s wurzel=%d neu=%d %v ; ueberlebende nach CloseHandle=%v ; hintergrund-ignoriert=%v\n",
		name, status, rootPid, len(created), seen, survivors, ignored)
}
