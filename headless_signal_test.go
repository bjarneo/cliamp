//go:build unix

package main

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// quitRecorder quits on playback.QuitMsg, as the Model does after it keeps
// the resume position.
type quitRecorder struct {
	started chan struct{}
	quit    bool
}

func (r quitRecorder) Init() tea.Cmd {
	close(r.started)
	return nil
}

func (r quitRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(playback.QuitMsg); ok {
		r.quit = true
		return r, tea.Quit
	}
	return r, nil
}

func (quitRecorder) View() tea.View { return tea.NewView("") }

// A real SIGINT, SIGTERM or SIGHUP reaches the Model as a quit message in
// headless mode and in the TUI, so the Model keeps the resume position. The
// program then ends with no error. The signal handler of Bubbletea would end
// the program with no Update, and on SIGINT with an error. A closed terminal
// sends SIGHUP.
func TestSignalQuitsThroughTheModel(t *testing.T) {
	// The TUI program runs with no terminal here, as headless mode does.
	noTerminal := []tea.ProgramOption{tea.WithInput(nil), tea.WithoutRenderer(), tea.WithOutput(io.Discard)}
	type signalCase struct {
		name    string
		options []tea.ProgramOption
		sig     syscall.Signal
	}
	var cases []signalCase
	for _, mode := range []signalCase{
		{name: "headless", options: programOptions(true, false)},
		{name: "TUI", options: append(programOptions(false, false), noTerminal...)},
		{name: "low-power TUI", options: append(programOptions(false, true), noTerminal...)},
	} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
			cases = append(cases, signalCase{name: mode.name + " " + sig.String(), options: mode.options, sig: sig})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			prog := tea.NewProgram(quitRecorder{started: started}, tc.options...)
			stop := quitOnSignals(prog.Send)
			defer stop()

			type result struct {
				model tea.Model
				err   error
			}
			done := make(chan result, 1)
			go func() {
				model, err := prog.Run()
				done <- result{model, err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				prog.Kill()
				t.Fatal("the program did not start")
			}

			if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
				prog.Kill()
				t.Fatal(err)
			}
			select {
			case res := <-done:
				if res.err != nil {
					t.Fatalf("Run error = %v, want a clean exit", res.err)
				}
				if !res.model.(quitRecorder).quit {
					t.Fatal("the model got no quit message")
				}
			case <-time.After(5 * time.Second):
				// The signal handler of Bubbletea can block the shutdown, so
				// Kill could block as well.
				t.Fatal("the program did not quit")
			}
		})
	}
}
