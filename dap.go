package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/go-dap"
	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
)

func dapServer(port string) error {
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	defer listener.Close()
	slog.Info("Started server", "addr", listener.Addr())

	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Error("Connection failed", "err", err)
			continue
		}
		slog.Info("Accepted connection", "remote", conn.RemoteAddr())
		// Handle multiple client connections concurrently
		go handleConnection(conn)
	}
}

func dapStdin() error {
	slog.Info("starting DAP using STDIN/STDOUT as communication protocol")
	debugSession := JsonnetDebugSession{
		rw:        bufio.NewReadWriter(bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)),
		sendQueue: make(chan dap.Message),
		stopDebug: make(chan struct{}),
		debugger:  jsonnet.MakeDebugger(),
	}

	go debugSession.sendFromQueue()
	go debugSession.dispatchEvents()

	for {
		err := debugSession.handleRequest()
		if err != nil {
			if err == io.EOF {
				slog.Debug("No more data to read", "err", err)
				break
			}
			// There maybe more messages to process, but
			// we will start with the strict behavior of only accepting
			// expected inputs.
			log.Fatal("Server error: ", err)
		}
	}

	close(debugSession.stopDebug)
	debugSession.sendWg.Wait()
	close(debugSession.sendQueue)
	return nil
}

func handleConnection(conn net.Conn) {
	debugSession := JsonnetDebugSession{
		rw:        bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
		sendQueue: make(chan dap.Message),
		stopDebug: make(chan struct{}),
		debugger:  jsonnet.MakeDebugger(),
	}

	go debugSession.sendFromQueue()
	go debugSession.dispatchEvents()

	for {
		err := debugSession.handleRequest()
		if err != nil {
			if err == io.EOF {
				slog.Debug("No more data to read", "err", err)
				break
			}
			// There maybe more messages to process, but
			// we will start with the strict behavior of only accepting
			// expected inputs.
			log.Fatal("Server error: ", err)
		}
	}

	slog.Debug("Closing connection", "remote", conn.RemoteAddr())
	close(debugSession.stopDebug)
	debugSession.sendWg.Wait()
	close(debugSession.sendQueue)
	conn.Close()
}

func (ds *JsonnetDebugSession) handleRequest() error {
	request, err := dap.ReadProtocolMessage(ds.rw.Reader)
	if err != nil {
		return err
	}
	slog.Debug("received request", "request", fmt.Sprintf("%#v", request))
	ds.sendWg.Add(1)
	go func() {
		ds.dispatchRequest(request)
		ds.sendWg.Done()
	}()
	return nil
}

func (ds *JsonnetDebugSession) dispatchEvents() {
	echan := ds.debugger.Events()
	var e dap.Message
	for {
		event := <-echan
		switch ev := event.(type) {
		case *jsonnet.DebugEventStop:
			ds.current = ev.Current
			switch ev.Reason {
			case jsonnet.StopReasonBreakpoint:
				e = &dap.StoppedEvent{
					Event: *newEvent("stopped"),
					Body:  dap.StoppedEventBody{Reason: "breakpoint", ThreadId: 1, AllThreadsStopped: true},
				}
			case jsonnet.StopReasonStep:
				e = &dap.StoppedEvent{
					Event: *newEvent("stopped"),
					Body:  dap.StoppedEventBody{Reason: "step", ThreadId: 1, AllThreadsStopped: true},
				}
			case jsonnet.StopReasonException:
				e = &dap.StoppedEvent{
					Event: *newEvent("stopped"),
					Body:  dap.StoppedEventBody{Reason: "exception", ThreadId: 1, AllThreadsStopped: true, Text: ev.Error.Error()},
				}
			}
		case *jsonnet.DebugEventExit:
			e = &dap.TerminatedEvent{
				Event: *newEvent("terminated"),
			}
		}
		ds.send(e)
	}
}

// dispatchRequest launches a new goroutine to process each request
// and send back events and responses.
func (ds *JsonnetDebugSession) dispatchRequest(request dap.Message) {
	switch request := request.(type) {
	case *dap.InitializeRequest:
		ds.onInitializeRequest(request)
	case *dap.LaunchRequest:
		ds.onLaunchRequest(request)
	case *dap.AttachRequest:
		ds.onAttachRequest(request)
	case *dap.DisconnectRequest:
		ds.onDisconnectRequest(request)
	case *dap.TerminateRequest:
		ds.onTerminateRequest(request)
	case *dap.RestartRequest:
		ds.onRestartRequest(request)
	case *dap.SetBreakpointsRequest:
		ds.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest:
		ds.onSetFunctionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest:
		ds.onSetExceptionBreakpointsRequest(request)
	case *dap.ConfigurationDoneRequest:
		ds.onConfigurationDoneRequest(request)
	case *dap.ContinueRequest:
		ds.onContinueRequest(request)
	case *dap.NextRequest:
		ds.onNextRequest(request)
	case *dap.StepInRequest:
		ds.onStepInRequest(request)
	case *dap.StepOutRequest:
		ds.onStepOutRequest(request)
	case *dap.StepBackRequest:
		ds.onStepBackRequest(request)
	case *dap.ReverseContinueRequest:
		ds.onReverseContinueRequest(request)
	case *dap.RestartFrameRequest:
		ds.onRestartFrameRequest(request)
	case *dap.GotoRequest:
		ds.onGotoRequest(request)
	case *dap.PauseRequest:
		ds.onPauseRequest(request)
	case *dap.StackTraceRequest:
		ds.onStackTraceRequest(request)
	case *dap.ScopesRequest:
		ds.onScopesRequest(request)
	case *dap.VariablesRequest:
		ds.onVariablesRequest(request)
	case *dap.SetVariableRequest:
		ds.onSetVariableRequest(request)
	case *dap.SetExpressionRequest:
		ds.onSetExpressionRequest(request)
	case *dap.SourceRequest:
		ds.onSourceRequest(request)
	case *dap.ThreadsRequest:
		ds.onThreadsRequest(request)
	case *dap.TerminateThreadsRequest:
		ds.onTerminateThreadsRequest(request)
	case *dap.EvaluateRequest:
		ds.onEvaluateRequest(request)
	case *dap.StepInTargetsRequest:
		ds.onStepInTargetsRequest(request)
	case *dap.GotoTargetsRequest:
		ds.onGotoTargetsRequest(request)
	case *dap.CompletionsRequest:
		ds.onCompletionsRequest(request)
	case *dap.ExceptionInfoRequest:
		ds.onExceptionInfoRequest(request)
	case *dap.LoadedSourcesRequest:
		ds.onLoadedSourcesRequest(request)
	case *dap.DataBreakpointInfoRequest:
		ds.onDataBreakpointInfoRequest(request)
	case *dap.SetDataBreakpointsRequest:
		ds.onSetDataBreakpointsRequest(request)
	case *dap.ReadMemoryRequest:
		ds.onReadMemoryRequest(request)
	case *dap.DisassembleRequest:
		ds.onDisassembleRequest(request)
	case *dap.CancelRequest:
		ds.onCancelRequest(request)
	case *dap.BreakpointLocationsRequest:
		ds.onBreakpointLocationsRequest(request)
	default:
		log.Fatalf("Unable to process %#v", request)
	}
}

// send lets the sender goroutine know via a channel that there is
// a message to be sent to client. This is called by per-request
// goroutines to send events and responses for each request and
// to notify of events triggered by the fake debugger.
func (ds *JsonnetDebugSession) send(message dap.Message) {
	ds.sendQueue <- message
}

// sendFromQueue is to be run in a separate goroutine to listen on a
// channel for messages to send back to the client. It will
// return once the channel is closed.
func (ds *JsonnetDebugSession) sendFromQueue() {
	for message := range ds.sendQueue {
		dap.WriteProtocolMessage(ds.rw.Writer, message)
		slog.Debug("message sent", "data", message)
		ds.rw.Flush()
	}
}

// -----------------------------------------------------------------------
// Very Fake Debugger
//

// The debugging session will keep track of how many breakpoints
// have been set. Once start-up is done (i.e. configurationDone
// request is processed), it will "stop" at each breakpoint one by
// one, and once there are no more, it will trigger a terminated event.
type JsonnetDebugSession struct {
	// rw is used to read requests and write events/responses
	rw *bufio.ReadWriter

	// sendQueue is used to capture messages from multiple request
	// processing goroutines while writing them to the client connection
	// from a single goroutine via sendFromQueue. We must keep track of
	// the multiple channel senders with a wait group to make sure we do
	// not close this channel prematurely. Closing this channel will signal
	// the sendFromQueue goroutine that it can exit.
	sendQueue chan dap.Message
	sendWg    sync.WaitGroup

	// stopDebug is used to notify long-running handlers to stop processing.
	stopDebug chan struct{}

	// bpSet is a counter of the remaining breakpoints that the debug
	// session is yet to stop at before the program terminates.
	bpSet    int
	bpSetMux sync.Mutex

	debugger *jsonnet.Debugger
	current  ast.Node
}

// -----------------------------------------------------------------------
// Request Handlers
//
// Below is a dummy implementation of the request handlers.
// They take no action, but just return dummy responses.
// A real debug adaptor would call the debugger methods here
// and use their results to populate each response.

func (ds *JsonnetDebugSession) onInitializeRequest(request *dap.InitializeRequest) {

	response := &dap.InitializeResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body.SupportsConfigurationDoneRequest = false
	response.Body.SupportsFunctionBreakpoints = false
	response.Body.SupportsConditionalBreakpoints = false
	response.Body.SupportsHitConditionalBreakpoints = false
	response.Body.SupportsEvaluateForHovers = false
	response.Body.ExceptionBreakpointFilters = []dap.ExceptionBreakpointsFilter{}
	response.Body.SupportsStepBack = false
	response.Body.SupportsSetVariable = false
	response.Body.SupportsRestartFrame = false
	response.Body.SupportsGotoTargetsRequest = false
	response.Body.SupportsStepInTargetsRequest = false
	response.Body.SupportsCompletionsRequest = false
	response.Body.CompletionTriggerCharacters = []string{}
	response.Body.SupportsModulesRequest = false
	response.Body.AdditionalModuleColumns = []dap.ColumnDescriptor{}
	response.Body.SupportedChecksumAlgorithms = []dap.ChecksumAlgorithm{}
	response.Body.SupportsRestartRequest = false
	response.Body.SupportsExceptionOptions = false
	response.Body.SupportsValueFormattingOptions = false
	response.Body.SupportsExceptionInfoRequest = false
	response.Body.SupportTerminateDebuggee = false
	response.Body.SupportsDelayedStackTraceLoading = false
	response.Body.SupportsLoadedSourcesRequest = false
	response.Body.SupportsLogPoints = false
	response.Body.SupportsTerminateThreadsRequest = false
	response.Body.SupportsSetExpression = false
	response.Body.SupportsTerminateRequest = false
	response.Body.SupportsDataBreakpoints = false
	response.Body.SupportsReadMemoryRequest = false
	response.Body.SupportsDisassembleRequest = false
	response.Body.SupportsCancelRequest = false
	response.Body.SupportsBreakpointLocationsRequest = false

	// This is a fake set up, so we can start "accepting" configuration
	// requests for setting breakpoints, etc from the client at any time.
	// Notify the client with an 'initialized' event. The client will end
	// the configuration sequence with 'configurationDone' request.
	e := &dap.InitializedEvent{Event: *newEvent("initialized")}
	ds.send(e)
	ds.send(response)
}

type launchRequest struct {
	Program string   `json:"program"`
	JPaths  []string `json:"jpaths"`
}

func (ds *JsonnetDebugSession) onLaunchRequest(request *dap.LaunchRequest) {
	log.Printf("Received launch request: %s\n", request.Arguments)
	lr := launchRequest{}
	err := json.Unmarshal(request.Arguments, &lr)
	if err != nil {
		ds.send(newErrorResponse(request.Seq, request.Command, "Invalid launch arguments"))
		return
	}
	raw, err := os.ReadFile(lr.Program)
	if err != nil {
		ds.send(newErrorResponse(request.Seq, request.Command, "Failed to open file: "+err.Error()))
		return
	}
	ds.debugger.Launch(lr.Program, string(raw), lr.JPaths)
	slog.Debug("Starting debugging", "breakpoints", ds.debugger.ActiveBreakpoints(), "file", lr.Program)
	response := &dap.LaunchResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onAttachRequest(request *dap.AttachRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "AttachRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onDisconnectRequest(request *dap.DisconnectRequest) {
	response := &dap.DisconnectResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onTerminateRequest(request *dap.TerminateRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "TerminateRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onRestartRequest(request *dap.RestartRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "RestartRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	response := &dap.SetBreakpointsResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body.Breakpoints = make([]dap.Breakpoint, len(request.Arguments.Breakpoints))
	ds.debugger.ClearBreakpoints(request.Arguments.Source.Path)
	for i, b := range request.Arguments.Breakpoints {
		_, err := ds.debugger.SetBreakpoint(request.Arguments.Source.Path, b.Line, -1)
		if err != nil {
			slog.Error("failed to set breakpoint", "err", err)
			continue
		}
		response.Body.Breakpoints[i].Line = b.Line
		response.Body.Breakpoints[i].Verified = true
	}
	ds.send(response)
}

func (ds *JsonnetDebugSession) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "SetFunctionBreakpointsRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onSetExceptionBreakpointsRequest(request *dap.SetExceptionBreakpointsRequest) {
	response := &dap.SetExceptionBreakpointsResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "ConfigurationDoneRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onContinueRequest(request *dap.ContinueRequest) {
	ds.debugger.Continue()
	response := &dap.ContinueResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onNextRequest(request *dap.NextRequest) {
	ds.debugger.ContinueUntilAfter(ds.current)
	response := &dap.NextResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onStepInRequest(request *dap.StepInRequest) {
	ds.debugger.Step()
	response := &dap.StepInResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	ds.send(response)
}

func (ds *JsonnetDebugSession) onStepOutRequest(request *dap.StepOutRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "StepOutRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onStepBackRequest(request *dap.StepBackRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "StepBackRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onReverseContinueRequest(request *dap.ReverseContinueRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "ReverseContinueRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onRestartFrameRequest(request *dap.RestartFrameRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "RestartFrameRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onGotoRequest(request *dap.GotoRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "GotoRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onPauseRequest(request *dap.PauseRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "PauseRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onStackTraceRequest(request *dap.StackTraceRequest) {
	trace := ds.debugger.StackTrace()
	response := &dap.StackTraceResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	frames := []dap.StackFrame{}
	for i, frame := range trace {
		fr := dap.StackFrame{
			Id:   i,
			Name: frame.Name,
		}
		if frame.Loc.File != nil {
			abs, err := filepath.Abs(string(frame.Loc.File.DiagnosticFileName))
			if err != nil {
				slog.Error("invalid location for stack frame")
				continue
			}
			fr.Source = &dap.Source{Name: string(frame.Loc.File.DiagnosticFileName), Path: abs, SourceReference: 0}
			fr.Line = frame.Loc.Begin.Line
			fr.Column = frame.Loc.Begin.Column
			fr.EndLine = frame.Loc.End.Line
			fr.EndColumn = frame.Loc.End.Column
		}
		if strings.HasPrefix(fr.Name, "/") {
			fr.Name = filepath.Base(fr.Name)
		}
		frames = append([]dap.StackFrame{fr}, frames...)
	}
	response.Body = dap.StackTraceResponseBody{
		StackFrames: frames,
		TotalFrames: len(frames),
	}
	ds.send(response)
}

func (ds *JsonnetDebugSession) onScopesRequest(request *dap.ScopesRequest) {
	response := &dap.ScopesResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.ScopesResponseBody{
		Scopes: []dap.Scope{
			{Name: "Local", VariablesReference: 1000, Expensive: false},
		},
	}
	ds.send(response)
}

func (ds *JsonnetDebugSession) onVariablesRequest(request *dap.VariablesRequest) {
	vars := ds.debugger.ListVars()
	selfPresent := false
	for _, v := range vars {
		if v == "self" {
			selfPresent = true
		}
	}
	if !selfPresent {
		vars = append(vars, "self")
	}
	out := []dap.Variable{}
	for _, v := range vars {
		val, err := ds.debugger.LookupValue(string(v))
		if err != nil {
			slog.Warn("Failed to get value for variable listing", "var", v, "err", err)
			val = ""
		}
		if string(v) == "self" {
			selfPresent = true
		}
		out = append(out, dap.Variable{
			Name:         string(v),
			Value:        val,
			EvaluateName: string(v),
		})
	}
	response := &dap.VariablesResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.VariablesResponseBody{
		Variables: out,
	}
	ds.send(response)
}

func (ds *JsonnetDebugSession) onSetVariableRequest(request *dap.SetVariableRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "setVariableRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onSetExpressionRequest(request *dap.SetExpressionRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "SetExpressionRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onSourceRequest(request *dap.SourceRequest) {
	slog.Debug("source requested", "source", request.Arguments.Source.SourceReference)
	ds.send(newErrorResponse(request.Seq, request.Command, "SourceRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onThreadsRequest(request *dap.ThreadsRequest) {
	response := &dap.ThreadsResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.ThreadsResponseBody{Threads: []dap.Thread{{Id: 1, Name: "main"}}}
	ds.send(response)

}

func (ds *JsonnetDebugSession) onTerminateThreadsRequest(request *dap.TerminateThreadsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "TerminateRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onEvaluateRequest(request *dap.EvaluateRequest) {
	v, err := ds.debugger.LookupValue(request.Arguments.Expression)
	if err != nil {
		ds.send(newErrorResponse(request.Seq, request.Command, fmt.Sprintf("Failed to look up variable: %s", err.Error())))
		return
	}
	response := &dap.EvaluateResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.EvaluateResponseBody{
		Result: v,
		Type:   "string",
	}
	ds.send(response)
}

func (ds *JsonnetDebugSession) onStepInTargetsRequest(request *dap.StepInTargetsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "StepInTargetRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onGotoTargetsRequest(request *dap.GotoTargetsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "GotoTargetRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onCompletionsRequest(request *dap.CompletionsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "CompletionRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onExceptionInfoRequest(request *dap.ExceptionInfoRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "ExceptionRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onLoadedSourcesRequest(request *dap.LoadedSourcesRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "LoadedRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onDataBreakpointInfoRequest(request *dap.DataBreakpointInfoRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "DataBreakpointInfoRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onSetDataBreakpointsRequest(request *dap.SetDataBreakpointsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "SetDataBreakpointsRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onReadMemoryRequest(request *dap.ReadMemoryRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "ReadMemoryRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onDisassembleRequest(request *dap.DisassembleRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "DisassembleRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onCancelRequest(request *dap.CancelRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "CancelRequest is not yet supported"))
}

func (ds *JsonnetDebugSession) onBreakpointLocationsRequest(request *dap.BreakpointLocationsRequest) {
	ds.send(newErrorResponse(request.Seq, request.Command, "BreakpointLocationsRequest is not yet supported"))
}

func newEvent(event string) *dap.Event {
	return &dap.Event{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "event",
		},
		Event: event,
	}
}

func newResponse(requestSeq int, command string) *dap.Response {
	return &dap.Response{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "response",
		},
		Command:    command,
		RequestSeq: requestSeq,
		Success:    true,
	}
}

func newErrorResponse(requestSeq int, command string, message string) *dap.ErrorResponse {
	er := &dap.ErrorResponse{}
	er.Response = *newResponse(requestSeq, command)
	er.Success = false
	er.Message = "unsupported"
	er.Body = dap.ErrorResponseBody{
		Error: &dap.ErrorMessage{},
	}
	er.Body.Error.Format = message
	er.Body.Error.Id = 12345
	return er
}
