package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
	"github.com/gookit/color"
	"github.com/peterh/liner"
)

type ReplDebugger struct {
	dbg      *jsonnet.Debugger
	line     *liner.State
	histFile string
	raw      string
	filename string
	jpaths   []string
}

func MakeReplDebugger(filename, snippet string, jpaths []string) *ReplDebugger {
	line := liner.NewLiner()
	line.SetCtrlCAborts(true)
	histFile := filepath.Join(os.TempDir(), ".jsonnice-history")
	if f, err := os.Open(histFile); err == nil {
		line.ReadHistory(f)
		f.Close()
	}
	dbg := jsonnet.MakeDebugger()
	return &ReplDebugger{
		line:     line,
		dbg:      dbg,
		histFile: histFile,
		raw:      snippet,
		filename: filename,
		jpaths:   jpaths,
	}
}

func (r *ReplDebugger) Run() {
	defer r.line.Close()
	events := r.dbg.Events()
	r.repl(nil, nil, nil)
EVENTLOOP:
	for {
		msg := <-events
		slog.Info("received event", "type", fmt.Sprintf("%T", msg))
		switch e := msg.(type) {
		case *jsonnet.DebugEventExit:
			if e.Output != "" {
				fmt.Println(e.Output)
			}
			if e.Error != nil {
				fmt.Printf("Error during evaluation: %s\n", e.Error.Error())
			}
			break EVENTLOOP
		case *jsonnet.DebugEventStop:
			switch e.Reason {
			case jsonnet.StopReasonBreakpoint:
				color.Bold.Print("Hit breakpoint: ")
				color.OpUnderscore.Println(e.Breakpoint)
				r.printCurrentContext(e.Current)
			case jsonnet.StopReasonStep:
				r.printCurrentContext(e.Current)
			case jsonnet.StopReasonException:
				fmt.Printf("%s: %s\n", color.Red.Render("Encountered error during evaluation"), e.ErrorFmt())
				r.printCurrentContext(e.Current)
			}
			r.repl(e.Current, e.LastEvaluation, e.Error)
		}
	}
	if f, err := os.Create(r.histFile); err == nil {
		r.line.WriteHistory(f)
		f.Close()
	}
}

func (d *ReplDebugger) printCurrentContext(current ast.Node) {
	lines := strings.Split(d.raw, "\n")
	lines = append([]string{""}, current.Loc().File.Lines...)
	clines := 3 // how many lines of context to show
	loc := current.Loc()
	for i := loc.Begin.Line - clines; i < loc.Begin.Line; i++ {
		if i < 1 {
			continue
		}
		color.Grayf("%2d| ", i)
		fmt.Printf("%s", lines[i])
	}
	color.Grayf("%2d| ", loc.Begin.Line)
	fmt.Printf("%s", lines[loc.Begin.Line][0:loc.Begin.Column-1])
	if loc.Begin.Line == loc.End.Line {
		color.Bluef("%s", lines[loc.Begin.Line][loc.Begin.Column-1:loc.End.Column-1])
		fmt.Printf("%s", lines[loc.Begin.Line][loc.End.Column-1:])
	} else {
		color.Bluef("%s", lines[loc.Begin.Line][loc.Begin.Column-1:])
		for i := loc.Begin.Line + 1; i < loc.End.Line; i++ {
			color.Grayf("%2d| ", i)
			color.Bluef("%s", lines[i])
		}
		color.Grayf("%2d| ", loc.End.Line)
		color.Bluef("%s", lines[loc.End.Line][:loc.End.Column-1])
		fmt.Printf("%s", lines[loc.End.Line][loc.End.Column:])
	}
	for i := loc.End.Line + 1; i < loc.End.Line+1+clines; i++ {
		if i >= len(lines) {
			continue
		}
		color.Grayf("%2d| ", i)
		fmt.Printf("%s", lines[i])
	}
}

func (r *ReplDebugger) repl(current ast.Node, lastVal *string, jerr error) {
	p := "> "
	if current != nil {
		p = fmt.Sprintf("%s [%T]> ", current.Loc().String(), current)
	}
	if jerr != nil {
		fmt.Print(color.Red.Render("! "))
	}
	r.line.SetCompleter(func(line string) (c []string) {
		parts := strings.Split(line, " ")
		switch parts[0] {
		case "b", "break":
			if len(parts) < 2 {
				return
			}
			loc, err := r.dbg.BreakpointLocations(r.filename)
			if err != nil {
				slog.Warn("Unable to autocomplete breakpoints", "err", err)
			}
			for _, l := range loc {
				if strings.HasPrefix(l.String(), parts[1]) {
					c = append(c, fmt.Sprintf("%s %s:%s", parts[0], l.File.DiagnosticFileName, l.Begin.String()))
				}
			}
		}
		return
	})
	input, err := r.line.Prompt(p)
	if err == liner.ErrPromptAborted {
		os.Exit(1)
	}

	r.line.AppendHistory(input)
	parts := strings.Split(string(input), " ")
	switch parts[0] {
	case "b", "break":
		if len(parts) < 2 {
			for _, b := range r.dbg.ActiveBreakpoints() {
				fmt.Printf("- %s\n", b)
			}
			break
		}
		binfo := strings.Split(parts[1], ":")
		if len(binfo) < 2 {
			fmt.Println("Must specify file and line separated by `:`")
			break
		}
		line, err := strconv.Atoi(binfo[1])
		if err != nil {
			fmt.Printf("Invalid line number: %s\n", err.Error())
			break
		}
		column := -1
		if len(binfo) == 3 {
			cint, err := strconv.Atoi(binfo[2])
			if err != nil {
				fmt.Printf("Invalid column number: %s\n", err.Error())
				break
			}
			column = cint
		}
		if target, err := r.dbg.SetBreakpoint(binfo[0], line, column); err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("Adding breakpoint at %s\n", target)
		}
	case "n", "next":
		r.dbg.ContinueUntilAfter(current)
		return
	case "s":
		r.dbg.Step()
		return
	case "l":
		if current != nil {
			r.printCurrentContext(current)
		} else {
			r.printFile()
		}
	case "lb", "lbs": // list possible breakpoints
		loc, err := r.dbg.BreakpointLocations(r.filename)
		if err != nil {
			slog.Warn("Unable to autocomplete breakpoints", "err", err)
		}
		for _, l := range loc {
			fmt.Printf("- %s:%s\n", l.File.DiagnosticFileName, l.Begin.String())
		}
	case "p":
		if len(parts) < 2 {
			parts = append(parts, "self")
		}
		val, err := r.dbg.LookupValue(parts[1])
		if err != nil {
			fmt.Println(err.Error())
		} else {
			fmt.Println(val)
		}
	case "trace":
		tr := r.dbg.StackTrace()
		for _, frame := range tr {
			fmt.Printf("- %s", frame.Name)
			if frame.Loc.File != nil {
				fmt.Print("\t\t\t")
				fmt.Print(color.Gray.Render(fmt.Sprintf("%s:%d:%d", frame.Loc.File.DiagnosticFileName, frame.Loc.Begin.Line, frame.Loc.Begin.Column)))
			}
			fmt.Print("\n")
		}
	case "last":
		if lastVal != nil {
			fmt.Printf("Last evaluation: %s\n", color.Magenta.Render(*lastVal))
		}
	case "vars":
		vars := r.dbg.ListVars()
		fmt.Printf("Variables:\n")
		for _, v := range vars {
			fmt.Printf("- %s\n", v)
		}
	case "q":
		r.dbg.Terminate()
		return
	case "clear":
		r.dbg.ClearBreakpoints(parts[1])
	case "c":
		if current == nil {
			r.dbg.Launch(r.filename, r.raw, r.jpaths)
		} else {
			r.dbg.Continue()
		}
		return
	case "":
	default:
		fmt.Printf("Unknonw command: %s\n", input)
	}
	r.repl(current, nil, jerr)
}

func (r *ReplDebugger) printFile() {
	fmt.Printf("File: %s\n", color.FgBlue.Render(r.filename))
	lines := strings.Split(r.raw, "\n")
	lines = append([]string{""}, lines...)
	for i, l := range lines {
		if i == 0 {
			continue
		}
		color.Grayf("%2d| ", i)
		fmt.Printf("%s\n", l)
	}
}
