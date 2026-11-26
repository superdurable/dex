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

// goExternalCallFinder fills methodExternalCalls for every Go Step method with an allowlist.
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

func (finder *goExternalCallFinder) record() {
	for _, handler := range finder.analyzer.handlerSummaries {
		if handler.phase != "execute" && handler.phase != "wait_for" {
			continue
		}
		if handler.method == nil || handler.method.Body == nil {
			continue
		}
		calls, _ := finder.bodyExternalCalls(handler.method.Body, 0, false)
		finder.analyzer.graph.methodExternalCalls = append(finder.analyzer.graph.methodExternalCalls, methodExternalCalls{
			NodeID: handler.ownerID, Phase: handler.phase, Calls: calls,
		})
	}
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
}

// bodyExternalCalls returns the possible external calls in body, and whether the result is exact.
// When firstOnly is set it stops at the first call found.
func (finder *goExternalCallFinder) bodyExternalCalls(body ast.Node, depth int, firstOnly bool) ([]possibleExternalCall, bool) {
	scan := &goExternalCallScan{
		finder: finder, depth: depth, firstOnly: firstOnly, calls: make([]possibleExternalCall, 0), isExact: true,
		calleeIdentifiers: make(map[*ast.Ident]bool), selectors: make(map[*ast.Ident]*ast.SelectorExpr),
	}
	ast.Inspect(body, scan.visit)
	return scan.calls, scan.isExact
}

func (scan *goExternalCallScan) visit(node ast.Node) bool {
	if scan.firstOnly && len(scan.calls) > 0 {
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
	external, isExact := scan.finder.classify(scan.finder.analyzer.expressionString(calleeExpression), function, scan.depth)
	scan.isExact = scan.isExact && isExact
	if external == nil {
		return
	}
	external.Span = scan.finder.analyzer.span(node)
	scan.calls = append(scan.calls, *external)
}

// inspectCall classifies the callee of call; a function literal callee is walked as part of the body.
func (scan *goExternalCallScan) inspectCall(call *ast.CallExpr) {
	typeAndValue := scan.finder.analyzer.typeInfo.Types[call.Fun]
	if typeAndValue.IsType() || typeAndValue.IsBuiltin() {
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
func (finder *goExternalCallFinder) classify(callee string, function *types.Func, depth int) (*possibleExternalCall, bool) {
	if function == nil {
		return &possibleExternalCall{Callee: callee}, true
	}
	receiver := function.Signature().Recv()
	if isGoDexClientMethod(function, receiver) {
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
		return &possibleExternalCall{Callee: callee + " (calls " + inner.Callee + ")"}, isExact
	}
	if isGoPureStandardLibraryFunction(function) {
		return nil, true
	}
	return &possibleExternalCall{Callee: callee}, true
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
		return &possibleExternalCall{Callee: "a helper chain deeper than dexcli follows"}, false
	}
	finder.inProgress[function] = true
	calls, isExact := finder.bodyExternalCalls(declaration.Body, depth+1, true)
	delete(finder.inProgress, function)
	var result *possibleExternalCall
	if len(calls) > 0 {
		result = &calls[0]
	}
	if isExact {
		finder.exactResults[function] = result
	}
	return result, isExact
}

// isGoDexClientMethod reports a dex.Client method; it reaches the Dex server, so it counts as external.
func isGoDexClientMethod(function *types.Func, receiver *types.Var) bool {
	if receiver == nil || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage {
		return false
	}
	receiverType := receiver.Type()
	if pointer, isPointer := receiverType.(*types.Pointer); isPointer {
		receiverType = pointer.Elem()
	}
	named, isNamed := receiverType.(*types.Named)
	return isNamed && named.Obj().Name() == "Client"
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
