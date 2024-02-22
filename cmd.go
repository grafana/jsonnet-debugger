package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"

	"github.com/lmittmann/tint"
)

var (
	// Set with `-ldflags="-X 'main.version=<version>'"`
	version = "dev"
)

func printVersion(o io.Writer) {
	fmt.Fprintf(o, "jsonnet-debugger version %s\n", version)
}

func usage(o io.Writer) {
	printVersion(o)
	fmt.Fprintln(o)
	fmt.Fprintln(o, "jsonnice {<option>} { <filename> }")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "Available options:")
	fmt.Fprintln(o, "  -h / --help                This message")
	fmt.Fprintln(o, "  -e / --exec                Treat filename as code")
	fmt.Fprintln(o, "  -J / --jpath <dir>         Specify an additional library search dir")
	fmt.Fprintln(o, "  -d / --dap                 Start a debug-adapter-protocol server")
	fmt.Fprintln(o, "  -s / --stdin               Start a debug-adapter-protocol session using stdion/stdout for communication")
	fmt.Fprintln(o, "  -l / --log-level           Set the log level. Allowed values: debug,info,warn,error")
	fmt.Fprintln(o, "  --version                  Print version")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "In all cases:")
	fmt.Fprintln(o, "  Multichar options are expanded e.g. -abc becomes -a -b -c.")
	fmt.Fprintln(o, "  The -- option suppresses option processing for subsequent arguments.")
	fmt.Fprintln(o, "  Note that since filenames and jsonnet programs can begin with -, it is")
	fmt.Fprintln(o, "  advised to use -- if the argument is unknown, e.g. jsonnice -- \"$FILENAME\".")
}

type config struct {
	inputFile      string
	filenameIsCode bool
	dap            bool
	jpath          []string
	logLevel       slog.Level
	stdin          bool
}

type processArgsStatus int

const (
	processArgsStatusContinue     = iota
	processArgsStatusSuccessUsage = iota
	processArgsStatusFailureUsage = iota
	processArgsStatusSuccess      = iota
	processArgsStatusFailure      = iota
)

// nextArg retrieves the next argument from the commandline.
func nextArg(i *int, args []string) string {
	(*i)++
	if (*i) >= len(args) {
		fmt.Fprintln(os.Stderr, "Expected another commandline argument.")
		os.Exit(1)
	}
	return args[*i]
}

// simplifyArgs transforms an array of commandline arguments so that
// any -abc arg before the first -- (if any) are expanded into
// -a -b -c.
func simplifyArgs(args []string) (r []string) {
	r = make([]string, 0, len(args)*2)
	for i, arg := range args {
		if arg == "--" {
			for j := i; j < len(args); j++ {
				r = append(r, args[j])
			}
			break
		}
		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			for j := 1; j < len(arg); j++ {
				r = append(r, "-"+string(arg[j]))
			}
		} else {
			r = append(r, arg)
		}
	}
	return
}

func processArgs(givenArgs []string, config *config) (processArgsStatus, error) {
	args := simplifyArgs(givenArgs)

	remainingArgs := make([]string, 0, len(args))
	i := 0

	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			return processArgsStatusSuccessUsage, nil
		} else if arg == "-v" || arg == "--version" {
			printVersion(os.Stdout)
			return processArgsStatusSuccess, nil
		} else if arg == "-e" || arg == "--exec" {
			config.filenameIsCode = true
		} else if arg == "-s" || arg == "--stdin" {
			config.stdin = true
		} else if arg == "--" {
			// All subsequent args are not options.
			i++
			for ; i < len(args); i++ {
				remainingArgs = append(remainingArgs, args[i])
			}
			break
		} else if arg == "-J" || arg == "--jpath" {
			dir := nextArg(&i, args)
			if len(dir) == 0 {
				return processArgsStatusFailure, fmt.Errorf("-J argument was empty string")
			}
			config.jpath = append(config.jpath, dir)
		} else if arg == "-d" || arg == "--dap" {
			config.dap = true
		} else if arg == "-l" || arg == "--log-level" {
			level := nextArg(&i, args)
			if len(level) == 0 {
				return processArgsStatusFailure, fmt.Errorf("no log level specified")
			}
			slvl := slog.LevelError
			switch level {
			case "debug":
				slvl = slog.LevelDebug
			case "info":
				slvl = slog.LevelInfo
			case "warn":
				slvl = slog.LevelWarn
			case "error":
				slvl = slog.LevelError
			default:
				return processArgsStatusFailure, fmt.Errorf("invalid log level %s. Allowed: debug,info,warn,error", level)
			}
			config.logLevel = slvl
		} else if len(arg) > 1 && arg[0] == '-' {
			return processArgsStatusFailure, fmt.Errorf("unrecognized argument: %s", arg)
		} else {
			remainingArgs = append(remainingArgs, arg)
		}
	}

	if config.dap {
		return processArgsStatusContinue, nil
	}

	want := "filename"
	if config.filenameIsCode {
		want = "code"
	}
	if len(remainingArgs) == 0 {
		return processArgsStatusFailureUsage, fmt.Errorf("must give %s", want)
	}
	if len(remainingArgs) != 1 {
		// Should already have been caught by processArgs.
		panic("Internal error: expected a single input file.")
	}

	config.inputFile = remainingArgs[0]
	return processArgsStatusContinue, nil
}

// readInput gets Jsonnet code from the given place (file, commandline, stdin).
// It also updates the given filename to <stdin> or <cmdline> if it wasn't a
// real filename.
func readInput(filenameIsCode bool, filename *string) (input string, err error) {
	if filenameIsCode {
		input, err = *filename, nil
		*filename = "<cmdline>"
	} else if *filename == "-" {
		var bytes []byte
		bytes, err = io.ReadAll(os.Stdin)
		input = string(bytes)
		*filename = "<stdin>"
	} else {
		var bytes []byte
		bytes, err = os.ReadFile(*filename)
		input = string(bytes)
	}
	return
}

// safeReadInput runs ReadInput, exiting the process if there was a problem.
func safeReadInput(filenameIsCode bool, filename *string) string {
	output, err := readInput(filenameIsCode, filename)
	if err != nil {
		var op string
		switch typedErr := err.(type) {
		case *os.PathError:
			op = typedErr.Op
			err = typedErr.Err
		}
		if op == "open" {
			fmt.Fprintf(os.Stderr, "Opening input file: %s: %s\n", *filename, err.Error())
		} else if op == "read" {
			fmt.Fprintf(os.Stderr, "Reading input file: %s: %s\n", *filename, err.Error())
		} else {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		os.Exit(1)
	}
	return output
}

func main() {
	config := config{
		jpath:    []string{},
		logLevel: slog.LevelError,
	}
	status, err := processArgs(os.Args[1:], &config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: "+err.Error())
	}
	switch status {
	case processArgsStatusContinue:
		break
	case processArgsStatusSuccessUsage:
		usage(os.Stdout)
		os.Exit(0)
	case processArgsStatusFailureUsage:
		if err != nil {
			fmt.Fprintln(os.Stderr, "")
		}
		usage(os.Stderr)
		os.Exit(1)
	case processArgsStatusSuccess:
		os.Exit(0)
	case processArgsStatusFailure:
		os.Exit(1)
	}

	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level: config.logLevel,
	})))

	if config.dap {
		var err error
		if config.stdin {
			err = dapStdin()
		} else {
			err = dapServer("54321")
		}
		if err != nil {
			slog.Error("dap server terminated", "err", err)
		}
		return
	}

	inputFile := config.inputFile
	input := safeReadInput(config.filenameIsCode, &inputFile)
	if !config.filenameIsCode {
		config.jpath = append(config.jpath, path.Dir(inputFile))
	}
	repl := MakeReplDebugger(inputFile, input, config.jpath)
	repl.Run()
}
