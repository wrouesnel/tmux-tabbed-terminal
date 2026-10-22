package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPty is a ConPTY pseudo console. The console translates between the program's
// console API calls and the VT sequences on its pipes.
type conPty struct {
	console windows.Handle
	in      *os.File // writes to the console's input
	out     *os.File // reads the console's output
	once    sync.Once
}

func (p *conPty) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *conPty) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *conPty) Name() string                { return "" }

func (p *conPty) Resize(cols, rows int) error {
	//nolint:gosec // terminal sizes fit
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// Close closes the console, which ends its programs, and its pipes. The console's output
// pipe ends after what's left in it has been read.
func (p *conPty) Close() error {
	p.once.Do(func() {
		// ClosePseudoConsole waits for its output to be read on older Windows, so it
		// mustn't wait on the goroutine reading it.
		go func() {
			windows.ClosePseudoConsole(p.console)
			_ = p.out.Close()
		}()
		_ = p.in.Close()
	})
	return nil
}

func start(cmd *exec.Cmd, cols, rows int) (Pty, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	if cmd.Process != nil {
		return nil, errors.New("pty: already started")
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("creating the input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		closeHandle(inR)
		closeHandle(inW)
		return nil, fmt.Errorf("creating the output pipe: %w", err)
	}
	// The console keeps its own copies of its ends of the pipes.
	defer closeHandle(inR)
	defer closeHandle(outW)
	p := &conPty{in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}
	//nolint:gosec // terminal sizes fit
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, inR, outW, 0, &p.console); err != nil {
		_ = p.in.Close()
		_ = p.out.Close()
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	proc, err := startProcess(cmd, p.console)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	// The console's output doesn't end when its program does: close the console then, so
	// that reading it ends too.
	go func() {
		_, _ = windows.WaitForSingleObject(proc, windows.INFINITE)
		closeHandle(proc)
		_ = p.Close()
	}()
	return p, nil
}

// startProcess starts cmd attached to a pseudo console, which os/exec can't do, and sets
// cmd.Process so that cmd.Wait works. It returns a handle to the process, which the caller
// closes.
func startProcess(cmd *exec.Cmd, console windows.Handle) (windows.Handle, error) {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer to it. Reading the
	// handle's bits as a pointer passes it without converting a uintptr to a pointer.
	value := *(*unsafe.Pointer)(unsafe.Pointer(&console))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, value, unsafe.Sizeof(console)); err != nil {
		return 0, fmt.Errorf("setting the pseudo console attribute: %w", err)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))

	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return 0, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return 0, err
	}
	var dir *uint16
	if cmd.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return 0, err
		}
	}
	env, err := envBlock(cmd.Environ())
	if err != nil {
		return 0, err
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(app, cmdLine, nil, nil, false, flags, env, dir, &si.StartupInfo, &pi); err != nil {
		return 0, fmt.Errorf("starting %s: %w", cmd.Path, err)
	}
	closeHandle(pi.Thread)
	// Holding pi.Process keeps the process ID from being reused until FindProcess has its
	// own handle.
	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		closeHandle(pi.Process)
		return 0, fmt.Errorf("finding process %d: %w", pi.ProcessId, err)
	}
	cmd.Process = proc
	return pi.Process, nil
}

// envBlock returns an environment block for CreateProcess: NUL-terminated UTF-16
// "key=value" strings, ending with an empty one.
func envBlock(env []string) (*uint16, error) {
	var block []uint16
	for _, kv := range env {
		s, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		block = append(block, s...)
	}
	block = append(block, 0)
	if len(env) == 0 {
		block = append(block, 0)
	}
	return &block[0], nil
}

// closeHandle closes a handle that's finished with. There's nothing to do if that fails.
func closeHandle(h windows.Handle) { _ = windows.CloseHandle(h) }
