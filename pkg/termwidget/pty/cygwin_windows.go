package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// markerTimeout is how long Start waits for the pty's name. The shell names it as soon as
// it starts, before the program runs.
const markerTimeout = 10 * time.Second

// maxMarkerSearch is how much output is searched for the pty's name before giving up.
const maxMarkerSearch = 64 * 1024

// cygwinTools returns the paths of script and stty beside program, if it's a Cygwin or
// MSYS2 program, which have them and their runtime DLL beside them.
func cygwinTools(program string) (script, stty string, ok bool) {
	dir := filepath.Dir(program)
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if !exists("msys-2.0.dll") && !exists("cygwin1.dll") {
		return "", "", false
	}
	if !exists("script.exe") || !exists("stty.exe") {
		return "", "", false
	}
	return filepath.Join(dir, "script.exe"), filepath.Join(dir, "stty.exe"), true
}

// cygwinPty is a Cygwin pty that script runs a program on, over pipes to its input and
// output. It's resized with stty, which Cygwin passes to the program as on Unix.
type cygwinPty struct {
	in   *os.File
	out  *os.File
	stty string
	proc *os.Process
	// job holds script and every process it starts, and ends them when it's closed: by
	// Close, or by Windows when this process ends.
	job windows.Handle

	// ready is closed once the pty's name has been read from the output, or that's
	// given up. head is the output read until then, and headErr how reading it ended.
	ready   chan struct{}
	head    []byte
	headErr error

	mu      sync.Mutex
	name    string
	cols    int
	rows    int
	resized chan struct{}
	done    chan struct{}
	once    sync.Once
	outOnce sync.Once
}

func startCygwin(cmd *exec.Cmd, cols, rows int, script, stty string) (Pty, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating the input pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, fmt.Errorf("creating the output pipe: %w", err)
	}
	// script's own arguments are quoted, so that the Cygwin runtime doesn't expand them.
	//nolint:gosec // the program is the caller's
	sc := exec.Command(script, "-qfec", scriptCommand(cmd.Args, cols, rows), "/dev/null")
	sc.Env, sc.Dir = cmd.Env, cmd.Dir
	sc.Stdin, sc.Stdout, sc.Stderr = inR, outW, outW
	// script starts suspended, so that it's in its job before it can start anything, and
	// with its console window hidden, which a GUI program's console children would show.
	sc.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	err = sc.Start()
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, fmt.Errorf("starting %s: %w", script, err)
	}
	job, err := runInJob(sc.Process)
	if err != nil {
		_ = sc.Process.Kill()
		_ = inW.Close()
		_ = outR.Close()
		return nil, err
	}
	// The caller waits for cmd, which waits for the process.
	cmd.Process = sc.Process

	p := &cygwinPty{
		in: inW, out: outR, stty: stty, proc: sc.Process, job: job,
		ready: make(chan struct{}), cols: cols, rows: rows,
		resized: make(chan struct{}, 1), done: make(chan struct{}),
	}
	go p.readName()
	go p.resizer()
	select {
	case <-p.ready:
	case <-time.After(markerTimeout):
	}
	return p, nil
}

// ntResumeProcess resumes every thread of a process. os/exec doesn't return the thread
// handle ResumeThread would need.
var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// runInJob puts a suspended process in a new job, which ends it and everything it starts
// once the job's handle is closed, and resumes it.
func runInJob(proc *os.Process) (windows.Handle, error) {
	//nolint:gosec // process IDs fit
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|
		windows.PROCESS_SUSPEND_RESUME, false, uint32(proc.Pid))
	if err != nil {
		return 0, fmt.Errorf("opening process %d: %w", proc.Pid, err)
	}
	defer closeHandle(h)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		closeHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		closeHandle(job)
		return 0, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if status, _, _ := ntResumeProcess.Call(uintptr(h)); status != 0 {
		closeHandle(job)
		return 0, fmt.Errorf("NtResumeProcess: status %#x", status)
	}
	return job, nil
}

// readName reads the output until the pty's name has been taken out of it.
func (p *cygwinPty) readName() {
	defer close(p.ready)
	var buf []byte
	chunk := make([]byte, 4096) //nolint:mnd // a read's worth
	for {
		n, err := p.out.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if name, rest, found := cutMarker(buf); found {
			p.mu.Lock()
			p.name = name
			p.mu.Unlock()
			p.head = rest
			p.resize()
			return
		}
		if err != nil || len(buf) > maxMarkerSearch {
			p.head, p.headErr = buf, err
			return
		}
	}
}

// Read reads the program's output. Its pipe is closed once it ends.
func (p *cygwinPty) Read(b []byte) (int, error) {
	<-p.ready
	if len(p.head) > 0 {
		n := copy(b, p.head)
		p.head = p.head[n:]
		return n, nil
	}
	if p.headErr != nil {
		return 0, p.headErr
	}
	n, err := p.out.Read(b)
	if err != nil {
		p.outOnce.Do(func() { _ = p.out.Close() })
	}
	return n, err
}

func (p *cygwinPty) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *cygwinPty) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

// Resize sets the pty's size. stty sets it, in the background, so that a burst of
// resizes runs it only once or twice.
func (p *cygwinPty) Resize(cols, rows int) error {
	p.mu.Lock()
	p.cols, p.rows = cols, rows
	p.mu.Unlock()
	p.resize()
	return nil
}

// resize asks the resizer to set the pty's size.
func (p *cygwinPty) resize() {
	select {
	case p.resized <- struct{}{}:
	default:
	}
}

// resizer sets the pty's size with stty whenever it's changed and the pty's name is known.
func (p *cygwinPty) resizer() {
	setCols, setRows := 0, 0
	for {
		select {
		case <-p.done:
			return
		case <-p.resized:
		}
		p.mu.Lock()
		name, cols, rows := p.name, p.cols, p.rows
		p.mu.Unlock()
		if name == "" || (cols == setCols && rows == setRows) {
			continue
		}
		//nolint:gosec // stty is beside the program, and the name came from tty
		cmd := exec.Command(p.stty, "-F", name, "cols", strconv.Itoa(cols), "rows", strconv.Itoa(rows))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			setCols, setRows = cols, rows
		}
	}
}

// Close hangs the program up: closing its job ends script and everything it started.
func (p *cygwinPty) Close() error {
	var err error
	p.once.Do(func() {
		close(p.done)
		_ = p.in.Close()
		closeHandle(p.job)
		if kerr := p.proc.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
			err = kerr
		}
	})
	return err
}
