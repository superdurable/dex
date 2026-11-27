// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"go/ast"
	"go/types"
	"slices"
	"strings"
)

// goPureStandardLibraryPackages do no I/O; goDeniedStandardLibraryFunctions lists their exceptions.
var goPureStandardLibraryPackages = map[string]bool{
	"bytes": true, "cmp": true, "container/heap": true, "container/list": true, "container/ring": true,
	"context": true, "crypto/hmac": true, "crypto/md5": true, "crypto/sha1": true, "crypto/sha256": true,
	"crypto/sha512": true, "crypto/subtle": true, "encoding/base32": true, "encoding/base64": true,
	"encoding/binary": true, "encoding/hex": true, "encoding/json": true, "errors": true, "fmt": true,
	"hash": true, "hash/crc32": true, "hash/fnv": true, "hash/maphash": true, "html": true, "iter": true,
	"log": true, "log/slog": true, "maps": true, "math": true, "math/big": true, "math/bits": true,
	"math/cmplx": true, "math/rand": true, "math/rand/v2": true, "net/url": true, "path": true,
	"regexp": true, "slices": true, "sort": true, "strconv": true, "strings": true, "sync": true,
	"sync/atomic": true, "time": true, "unicode": true, "unicode/utf16": true, "unicode/utf8": true,
}

// goDeniedStandardLibraryFunctions block, wait, or read and write through an io.Reader or io.Writer.
var goDeniedStandardLibraryFunctions = map[string]bool{
	"encoding/json.NewDecoder": true, "encoding/json.NewEncoder": true,
	"fmt.Fprint": true, "fmt.Fprintf": true, "fmt.Fprintln": true, "fmt.Fscan": true, "fmt.Fscanf": true,
	"fmt.Fscanln": true, "fmt.Print": true, "fmt.Printf": true, "fmt.Println": true, "fmt.Scan": true,
	"fmt.Scanf": true, "fmt.Scanln": true,
	"time.After": true, "time.AfterFunc": true, "time.NewTicker": true, "time.NewTimer": true,
	"time.Sleep": true, "time.Tick": true,
}

// goDexSDKPackagePrefixes are the Dex SDK packages whose calls stay inside Dex.
var goDexSDKPackagePrefixes = []string{goSDKPackage, "github.com/superdurable/dex/sdk-go/logging"}

// goExternalCallFinder fills methodExternalCalls for Go Step methods with an allowlist.
type goExternalCallFinder struct {
	analyzer *goAnalyzer
	// exactResults caches helpers whose result did not depend on the depth bound or a cycle.
	exactResults map[*types.Func]*possibleExternalCall
	inProgress   map[*types.Func]bool
}

func newGoExternalCallFinder(analyzer *goAnalyzer) *goExternalCallFinder {
	return &goExternalCallFinder{
		analyzer:     analyzer,
		exactResults: make(map[*types.Func]*possibleExternalCall),
		inProgress:   make(map[*types.Func]bool),
	}
}

// record marks only the Step methods that share a lock with an RPC.
func (finder *goExternalCallFinder) record(stepLockEdges []Edge) {
	graph := finder.analyzer.graph
	shared := graph.stepMethodsSharingRPCLocks(graph.Edges, stepLockEdges)
	marked := make(map[string]bool)
	for _, handler := range finder.analyzer.handlerSummaries {
		key := stepMethodKey(handler.ownerID, handler.phase)
		if !shared[key] || marked[key] {
			continue
		}
		marked[key] = true
		calls := []possibleExternalCall{finder.unreadableBody(handler.ownerID, handler.phase)}
		if handler.method != nil && handler.method.Body != nil {
			calls, _ = finder.bodyExternalCalls(handler.method.Body, 0, false)
		}
		graph.methodExternalCalls = append(graph.methodExternalCalls, methodExternalCalls{NodeID: handler.ownerID, Phase: handler.phase, Calls: calls})
	}
	for stepType, nodeID := range finder.analyzer.steps {
		for _, phase := range []string{"execute", "wait_for"} {
			key := stepMethodKey(nodeID, phase)
			if !shared[key] || marked[key] || finder.isDexSDKStepMethod(stepType, phase) {
				continue
			}
			marked[key] = true
			graph.methodExternalCalls = append(graph.methodExternalCalls, methodExternalCalls{
				NodeID: nodeID, Phase: phase, Calls: []possibleExternalCall{finder.unreadableBody(nodeID, phase)},
			})
		}
	}
}

// unreadableBody is the unresolved call reported for a Step method whose body dexcli cannot read.
func (finder *goExternalCallFinder) unreadableBody(nodeID string, phase string) possibleExternalCall {
	node := finder.analyzer.node(nodeID)
	return possibleExternalCall{Callee: stepMethodLabel(phase) + " of " + node.Name + ", whose body dexcli cannot read", Span: node.Span, Certainty: externalCallUncertain}
}

// isDexSDKStepMethod reports whether the Step's method comes from an SDK default, such as dex.StepDefaults.
func (finder *goExternalCallFinder) isDexSDKStepMethod(stepType string, phase string) bool {
	valueType := finder.analyzer.stepValueTypes[stepType]
	if valueType == nil {
		return false
	}
	methodName := "Execute"
	if phase == "wait_for" {
		methodName = "WaitFor"
	}
	if _, isPointer := valueType.(*types.Pointer); !isPointer {
		valueType = types.NewPointer(valueType)
	}
	selection := types.NewMethodSet(valueType).Lookup(nil, methodName)
	if selection == nil {
		return false
	}
	method, isFunction := selection.Obj().(*types.Func)
	return isFunction && method.Pkg() != nil && isGoDexSDKPackage(method.Pkg().Path())
}

// goExternalCallScan collects the possible external calls of one function body.
type goExternalCallScan struct {
	finder    *goExternalCallFinder
	depth     int
	firstOnly bool
	calls     []possibleExternalCall
	isExact   bool
	// calleeIdentifiers are identifiers already classified as a call's callee.
	calleeIdentifiers map[*ast.Ident]bool
	selectors         map[*ast.Ident]*ast.SelectorExpr
	body              ast.Node
}

// bodyExternalCalls returns the possible external calls in body, and whether the result is exact.
// When firstOnly is set it stops at the first definite call; uncertain calls before it are kept.
func (finder *goExternalCallFinder) bodyExternalCalls(body ast.Node, depth int, firstOnly bool) ([]possibleExternalCall, bool) {
	scan := &goExternalCallScan{
		finder: finder, depth: depth, firstOnly: firstOnly, calls: make([]possibleExternalCall, 0), isExact: true,
		calleeIdentifiers: make(map[*ast.Ident]bool), selectors: make(map[*ast.Ident]*ast.SelectorExpr), body: body,
	}
	ast.Inspect(body, scan.visit)
	return scan.calls, scan.isExact
}

func (scan *goExternalCallScan) visit(node ast.Node) bool {
	if scan.firstOnly && slices.ContainsFunc(scan.calls, func(call possibleExternalCall) bool { return !call.isUncertain() }) {
		return false
	}
	switch current := node.(type) {
	case *ast.SelectorExpr:
		scan.selectors[current.Sel] = current
	case *ast.CallExpr:
		scan.inspectCall(current)
	case *ast.Ident:
		// A function used as a value, such as a callback argument, may run later.
		function, isFunction := scan.finder.analyzer.typeInfo.Uses[current].(*types.Func)
		if !isFunction || scan.calleeIdentifiers[current] {
			return true
		}
		valueExpression := ast.Expr(current)
		if selector := scan.selectors[current]; selector != nil {
			valueExpression = selector
		}
		scan.add(valueExpression, valueExpression, function.Origin())
	}
	return true
}

func (scan *goExternalCallScan) add(node ast.Node, calleeExpression ast.Expr, function *types.Func) {
	external, isExact := scan.finder.classify(scan.finder.analyzer.expressionString(calleeExpression), function, scan.depth, scan.isFieldReceiver(calleeExpression))
	scan.isExact = scan.isExact && isExact
	if external == nil {
		return
	}
	external.Span = scan.finder.analyzer.span(node)
	scan.calls = append(scan.calls, *external)
}

// isFieldReceiver reports whether a method callee runs on a struct field, such as step.gateway.Send, or on a local
// variable ever assigned from one, such as gateway := step.gateway; a field holds an injected service.
func (scan *goExternalCallScan) isFieldReceiver(calleeExpression ast.Expr) bool {
	selector, isSelector := ast.Unparen(calleeExpression).(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	return scan.isFieldValue(selector.X, 0)
}

func (scan *goExternalCallScan) isFieldValue(expression ast.Expr, depth int) bool {
	typeInfo := scan.finder.analyzer.typeInfo
	switch value := ast.Unparen(expression).(type) {
	case *ast.SelectorExpr:
		selection := typeInfo.Selections[value]
		return selection != nil && selection.Kind() == types.FieldVal
	case *ast.Ident:
		variable, isVariable := typeInfo.Uses[value].(*types.Var)
		if !isVariable || depth > goMaximumHelperDepth || scan.body == nil {
			return false
		}
		// Any assignment from a field marks the variable as an injected service, wherever it sits in the function.
		return slices.ContainsFunc(scan.localAssignments(variable), func(right ast.Expr) bool { return right != nil && scan.isFieldValue(right, depth+1) })
	}
	return false
}

// localAssignments returns every value assigned to variable in the scanned body; a nil entry is an assignment dexcli cannot pair.
func (scan *goExternalCallScan) localAssignments(variable *types.Var) []ast.Expr {
	typeInfo := scan.finder.analyzer.typeInfo
	var assigned []ast.Expr
	isVariable := func(expression ast.Expr) bool {
		identifier, isIdentifier := ast.Unparen(expression).(*ast.Ident)
		return isIdentifier && (typeInfo.Defs[identifier] == variable || typeInfo.Uses[identifier] == variable)
	}
	ast.Inspect(scan.body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, left := range statement.Lhs {
				if !isVariable(left) {
					continue
				}
				if len(statement.Lhs) == len(statement.Rhs) {
					assigned = append(assigned, statement.Rhs[index])
				} else {
					assigned = append(assigned, nil)
				}
			}
		case *ast.ValueSpec:
			for index, name := range statement.Names {
				if typeInfo.Defs[name] != variable {
					continue
				}
				if len(statement.Names) == len(statement.Values) {
					assigned = append(assigned, statement.Values[index])
				} else {
					assigned = append(assigned, nil)
				}
			}
		}
		return true
	})
	return assigned
}

// inspectCall classifies the callee of call; a function literal callee is walked as part of the body.
func (scan *goExternalCallScan) inspectCall(call *ast.CallExpr) {
	typeAndValue := scan.finder.analyzer.typeInfo.Types[call.Fun]
	if typeAndValue.IsBuiltin() {
		// The print and println built-ins write to standard error, which reaches no external system.
		if identifier, isIdentifier := ast.Unparen(call.Fun).(*ast.Ident); isIdentifier {
			if builtin, isBuiltin := scan.finder.analyzer.typeInfo.Uses[identifier].(*types.Builtin); isBuiltin && (builtin.Name() == "print" || builtin.Name() == "println") {
				scan.calls = append(scan.calls, possibleExternalCall{Callee: builtin.Name(), Span: scan.finder.analyzer.span(call), Certainty: externalCallUncertain})
			}
		}
		return
	}
	if typeAndValue.IsType() {
		return
	}
	callee := unwrapCallFun(ast.Unparen(call.Fun))
	var identifier *ast.Ident
	switch current := callee.(type) {
	case *ast.FuncLit:
		return
	case *ast.Ident:
		identifier = current
	case *ast.SelectorExpr:
		identifier = current.Sel
	}
	var function *types.Func
	if identifier != nil {
		scan.calleeIdentifiers[identifier] = true
		function, _ = scan.finder.analyzer.typeInfo.Uses[identifier].(*types.Func)
	}
	if function != nil {
		function = function.Origin()
	}
	scan.add(call, call.Fun, function)
}

// classify returns the possible external call for a callee, or nil, and whether the result is exact.
// A nil function is a dynamic call through a function value.
// A call into another package that may be pure is uncertain, unless isFieldReceiver marks an injected service.
func (finder *goExternalCallFinder) classify(callee string, function *types.Func, depth int, isFieldReceiver bool) (*possibleExternalCall, bool) {
	if function == nil {
		return &possibleExternalCall{Callee: callee}, true
	}
	receiver := function.Signature().Recv()
	if isGoDexServerFacingCall(function, receiver) {
		return &possibleExternalCall{Callee: callee}, true
	}
	if function.Pkg() == nil || isGoDexSDKPackage(function.Pkg().Path()) {
		return nil, true
	}
	if receiver != nil && types.IsInterface(receiver.Type()) {
		// dex.Context embeds context.Context, whose methods the Worker implements.
		if function.Pkg().Path() == "context" {
			return nil, true
		}
		return &possibleExternalCall{Callee: callee}, true
	}
	if declaration := finder.analyzer.functions.declarations[function]; declaration != nil {
		inner, isExact := finder.helperExternalCall(function, declaration, depth)
		if inner == nil {
			return nil, isExact
		}
		return &possibleExternalCall{Callee: callee + " (calls " + inner.Callee + ")", Certainty: inner.Certainty}, isExact
	}
	if isGoPureStandardLibraryFunction(function) {
		return nil, true
	}
	_, isModulePackage := finder.analyzer.modules[function.Pkg().Path()]
	if goPrintFunctions[function.Pkg().Path()+"."+function.Name()] || (!isFieldReceiver && isModulePackage && !isGoKnownInputOutputModule(function.Pkg().Path())) {
		return &possibleExternalCall{Callee: callee, Certainty: externalCallUncertain}, true
	}
	return &possibleExternalCall{Callee: callee}, true
}

// goPrintFunctions write to standard output, which reaches no external system.
var goPrintFunctions = map[string]bool{"fmt.Print": true, "fmt.Printf": true, "fmt.Println": true}

// goKnownInputOutputModulePrefixes are third-party modules that do network or database I/O.
var goKnownInputOutputModulePrefixes = []string{
	"cloud.google.com/go/", "github.com/aws/aws-sdk-go", "github.com/go-redis/", "github.com/go-sql-driver/mysql",
	"github.com/jackc/pgx", "github.com/lib/pq", "github.com/nats-io/", "github.com/redis/go-redis", "github.com/segmentio/kafka-go",
	"github.com/slack-go/", "github.com/stripe/", "github.com/twilio/", "go.mongodb.org/mongo-driver", "google.golang.org/grpc",
}

// isGoKnownInputOutputModule reports whether a package of a Go module, not the standard library, is known to do I/O.
func isGoKnownInputOutputModule(path string) bool {
	for _, prefix := range goKnownInputOutputModulePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// helperExternalCall returns the first possible external call of a same-package helper, and whether it is exact.
func (finder *goExternalCallFinder) helperExternalCall(function *types.Func, declaration *ast.FuncDecl, depth int) (*possibleExternalCall, bool) {
	if cached, isCached := finder.exactResults[function]; isCached {
		return cached, true
	}
	if finder.inProgress[function] {
		return nil, false
	}
	if depth+1 > goMaximumHelperDepth {
		return &possibleExternalCall{Callee: "a helper chain deeper than dexcli follows", Certainty: externalCallUncertain}, false
	}
	finder.inProgress[function] = true
	calls, isExact := finder.bodyExternalCalls(declaration.Body, depth+1, true)
	delete(finder.inProgress, function)
	var result *possibleExternalCall
	if index := slices.IndexFunc(calls, func(call possibleExternalCall) bool { return !call.isUncertain() }); index >= 0 {
		result = &calls[index]
	} else if len(calls) > 0 {
		result = &calls[0]
	}
	if isExact {
		finder.exactResults[function] = result
	}
	return result, isExact
}

// goDexServerFacingReceiverTypes reach the Dex server, read files, or run application callbacks.
var goDexServerFacingReceiverTypes = map[string]bool{
	"AESGCMPayloadCodec": true, "Client": true, "Credential": true, "FilePayloadEncryptionKeyProvider": true,
	"PayloadCodec": true, "PayloadEncryptionKeyProvider": true, "Worker": true,
}

// goDexServerFacingFunctions construct server connections or read files.
var goDexServerFacingFunctions = map[string]bool{
	"APIKeyCredential": true, "FileCredential": true, "NewClient": true, "NewFilePayloadEncryptionKeyProvider": true,
	"NewWorker": true, "ReadFlowDefinitionFiles": true,
}

// isGoDexServerFacingCall reports a Dex SDK entry outside the handler API; it counts as external.
func isGoDexServerFacingCall(function *types.Func, receiver *types.Var) bool {
	if function.Pkg() == nil || function.Pkg().Path() != goSDKPackage {
		return false
	}
	if receiver == nil {
		return goDexServerFacingFunctions[function.Name()]
	}
	receiverType := receiver.Type()
	if pointer, isPointer := receiverType.(*types.Pointer); isPointer {
		receiverType = pointer.Elem()
	}
	named, isNamed := receiverType.(*types.Named)
	return isNamed && goDexServerFacingReceiverTypes[named.Obj().Name()]
}

func isGoDexSDKPackage(path string) bool {
	for _, prefix := range goDexSDKPackagePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// isGoPureStandardLibraryFunction reports whether function is an allowlisted standard-library function or method.
func isGoPureStandardLibraryFunction(function *types.Func) bool {
	path := function.Pkg().Path()
	if !goPureStandardLibraryPackages[path] {
		return false
	}
	if function.Signature().Recv() != nil {
		// ReadFrom and WriteTo move data through a caller's io.Reader or io.Writer.
		return function.Name() != "ReadFrom" && function.Name() != "WriteTo"
	}
	return !goDeniedStandardLibraryFunctions[path+"."+function.Name()]
}
