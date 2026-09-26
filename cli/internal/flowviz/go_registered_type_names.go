// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type goRegisteredTypeNameResolver struct {
	packagesByTypes map[*types.Package]*packages.Package
}

type goRegisteredTypeName struct {
	name        string
	displayName string
}

type goTypeNameProblem struct {
	code    string
	message string
}

func newGoRegisteredTypeNameResolver(selectedPackage *packages.Package) *goRegisteredTypeNameResolver {
	resolver := &goRegisteredTypeNameResolver{packagesByTypes: make(map[*types.Package]*packages.Package)}
	resolver.indexPackages(selectedPackage, make(map[string]bool))
	return resolver
}

func (resolver *goRegisteredTypeNameResolver) indexPackages(currentPackage *packages.Package, visited map[string]bool) {
	if currentPackage == nil || visited[currentPackage.PkgPath] {
		return
	}
	visited[currentPackage.PkgPath] = true
	if currentPackage.Types != nil {
		resolver.packagesByTypes[currentPackage.Types] = currentPackage
	}
	for _, importedPackage := range currentPackage.Imports {
		resolver.indexPackages(importedPackage, visited)
	}
}

func (resolver *goRegisteredTypeNameResolver) resolveFlowTypeName(receiver *types.Named) (goRegisteredTypeName, *goTypeNameProblem) {
	// Registering a Flow by value cannot compile when an identity method needs a pointer receiver.
	flowValueType := types.NewPointer(receiver)
	override, problem := resolver.lookupIdentityOverride(flowValueType, "GetFlowType")
	if problem != nil {
		return goRegisteredTypeName{}, problem
	}
	if override != "" {
		return goRegisteredTypeName{name: override}, nil
	}
	if receiver.TypeParams().Len() > 0 {
		return goRegisteredTypeName{}, &goTypeNameProblem{
			code: "generic_flow_type_name",
			message: fmt.Sprintf("generic Flow %s must declare GetFlowType returning one non-empty compile-time string",
				goTypeLabel(receiver)),
		}
	}
	name, problem := resolver.sdkDefaultTypeName(flowValueType, "GetFlowType")
	if problem != nil {
		return goRegisteredTypeName{}, problem
	}
	return goRegisteredTypeName{name: name}, nil
}

// Default-named Steps also return their bare Go type as displayName.
func (resolver *goRegisteredTypeNameResolver) resolveStepTypeName(argumentType types.Type) (goRegisteredTypeName, *goTypeNameProblem) {
	override, problem := resolver.lookupIdentityOverride(argumentType, "GetStepType")
	if problem != nil {
		return goRegisteredTypeName{}, problem
	}
	if override != "" {
		return goRegisteredTypeName{name: override}, nil
	}
	name, problem := resolver.sdkDefaultTypeName(argumentType, "GetStepType")
	if problem != nil {
		return goRegisteredTypeName{}, problem
	}
	return goRegisteredTypeName{name: name, displayName: registeredNamedType(argumentType).Obj().Name()}, nil
}

// An empty override selects the SDK default name.
func (resolver *goRegisteredTypeNameResolver) lookupIdentityOverride(valueType types.Type, methodName string) (string, *goTypeNameProblem) {
	dynamicTypeName := &goTypeNameProblem{
		code:    "dynamic_type_name",
		message: fmt.Sprintf("%s of %s must return one compile-time string", methodName, goTypeLabel(valueType)),
	}
	selection := types.NewMethodSet(valueType).Lookup(nil, methodName)
	if selection == nil {
		return "", dynamicTypeName
	}
	method, isFunction := selection.Obj().(*types.Func)
	if !isFunction {
		return "", dynamicTypeName
	}
	if isSDKDefaultIdentityMethod(method) {
		return "", nil
	}
	if types.IsInterface(method.Signature().Recv().Type()) {
		return "", dynamicTypeName
	}
	declaration, info, found := resolver.identityMethodDeclaration(method)
	if !found {
		return "", dynamicTypeName
	}
	override, isConstant := resolver.constantReturnValue(declaration, info)
	if !isConstant {
		return "", dynamicTypeName
	}
	return override, nil
}

func isSDKDefaultIdentityMethod(method *types.Func) bool {
	if method.Pkg() == nil || method.Pkg().Path() != goSDKPackage {
		return false
	}
	receiver := registeredNamedType(method.Signature().Recv().Type())
	if receiver == nil {
		return false
	}
	return receiver.Obj().Name() == "FlowDefaults" || receiver.Obj().Name() == "DefaultStepType"
}

func (resolver *goRegisteredTypeNameResolver) identityMethodDeclaration(method *types.Func) (*ast.FuncDecl, *types.Info, bool) {
	origin := method.Origin()
	declaringPackage := resolver.packagesByTypes[origin.Pkg()]
	if declaringPackage == nil || declaringPackage.TypesInfo == nil {
		return nil, nil, false
	}
	for _, file := range declaringPackage.Syntax {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if isFunction && function.Name.Pos() == origin.Pos() {
				return function, declaringPackage.TypesInfo, true
			}
		}
	}
	return nil, nil, false
}

// Every return must yield one equal compile-time string; nested function literals are ignored.
func (resolver *goRegisteredTypeNameResolver) constantReturnValue(declaration *ast.FuncDecl, info *types.Info) (string, bool) {
	if declaration.Body == nil {
		return "", false
	}
	results := make([]ast.Expr, 0, 1)
	hasOneResultPerReturn := true
	ast.Inspect(declaration.Body, func(current ast.Node) bool {
		switch statement := current.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if len(statement.Results) != 1 {
				hasOneResultPerReturn = false
			} else {
				results = append(results, statement.Results[0])
			}
			return false
		}
		return true
	})
	if !hasOneResultPerReturn || len(results) == 0 {
		return "", false
	}
	value := ""
	for index, result := range results {
		current := ""
		if typeAndValue, found := info.Types[result]; found && typeAndValue.Value != nil && typeAndValue.Value.Kind() == constant.String {
			current = constant.StringVal(typeAndValue.Value)
		} else if literal, isLiteral := result.(*ast.BasicLit); isLiteral && literal.Kind == token.STRING {
			unquoted, err := strconv.Unquote(literal.Value)
			if err != nil {
				return "", false
			}
			current = unquoted
		} else {
			return "", false
		}
		if index > 0 && current != value {
			return "", false
		}
		value = current
	}
	return value, true
}

// sdkDefaultTypeName mirrors the Go SDK default: reflect.Type.String() without leading pointers.
func (resolver *goRegisteredTypeNameResolver) sdkDefaultTypeName(valueType types.Type, methodName string) (string, *goTypeNameProblem) {
	named := registeredNamedType(valueType)
	if named == nil {
		return "", &goTypeNameProblem{
			code:    "dynamic_type_name",
			message: fmt.Sprintf("%s has no package-qualified name; declare %s returning one compile-time string", goTypeLabel(valueType), methodName),
		}
	}
	name := named.Obj().Pkg().Name() + "." + named.Obj().Name()
	typeArguments := named.TypeArgs()
	if typeArguments.Len() == 0 {
		return name, nil
	}
	renderedArguments := make([]string, 0, typeArguments.Len())
	for index := 0; index < typeArguments.Len(); index++ {
		rendered, isSupported := reflectTypeArgumentString(typeArguments.At(index))
		if !isSupported {
			return "", &goTypeNameProblem{
				code: "unsupported_generic_type_name",
				message: fmt.Sprintf("%s type argument %s has no static Go reflection name; declare %s returning one compile-time string",
					goTypeLabel(valueType), goTypeLabel(typeArguments.At(index)), methodName),
			}
		}
		renderedArguments = append(renderedArguments, rendered)
	}
	return name + "[" + strings.Join(renderedArguments, ",") + "]", nil
}

func registeredNamedType(value types.Type) *types.Named {
	for {
		switch current := types.Unalias(value).(type) {
		case *types.Pointer:
			value = current.Elem()
		case *types.Named:
			if current.Obj() == nil || current.Obj().Pkg() == nil {
				return nil
			}
			return current
		default:
			return nil
		}
	}
}

func goTypeLabel(value types.Type) string {
	return strings.TrimLeft(types.TypeString(value, func(typePackage *types.Package) string { return typePackage.Name() }), "*")
}

// reflectTypeArgumentString renders a type argument as reflect.Type.String() does, where
// package-main types use the path "main".
func reflectTypeArgumentString(value types.Type) (string, bool) {
	switch current := types.Unalias(value).(type) {
	case *types.Named:
		object := current.Obj()
		if object.Pkg() == nil {
			return object.Name(), true
		}
		if object.Parent() != object.Pkg().Scope() {
			return "", false
		}
		packagePath := reflectPackagePath(object.Pkg().Path())
		if object.Pkg().Name() == "main" {
			packagePath = "main"
		}
		rendered := packagePath + "." + object.Name()
		typeArguments := current.TypeArgs()
		if typeArguments.Len() == 0 {
			return rendered, true
		}
		renderedArguments := make([]string, 0, typeArguments.Len())
		for index := 0; index < typeArguments.Len(); index++ {
			argument, isSupported := reflectTypeArgumentString(typeArguments.At(index))
			if !isSupported {
				return "", false
			}
			renderedArguments = append(renderedArguments, argument)
		}
		return rendered + "[" + strings.Join(renderedArguments, ",") + "]", true
	case *types.Basic:
		if current.Kind() == types.UnsafePointer {
			return "unsafe.Pointer", true
		}
		if current.Kind() == types.Invalid || current.Info()&types.IsUntyped != 0 {
			return "", false
		}
		return types.Typ[current.Kind()].Name(), true
	case *types.Interface:
		return "interface {}", current.Empty()
	case *types.Pointer:
		element, isSupported := reflectTypeArgumentString(current.Elem())
		return "*" + element, isSupported
	case *types.Slice:
		element, isSupported := reflectTypeArgumentString(current.Elem())
		return "[]" + element, isSupported
	case *types.Array:
		element, isSupported := reflectTypeArgumentString(current.Elem())
		return "[" + strconv.FormatInt(current.Len(), 10) + "]" + element, isSupported
	case *types.Map:
		key, isKeySupported := reflectTypeArgumentString(current.Key())
		element, isElementSupported := reflectTypeArgumentString(current.Elem())
		return "map[" + key + "]" + element, isKeySupported && isElementSupported
	}
	return "", false
}

// reflectPackagePath escapes a path as cmd/internal/objabi.PathToPrefix does; instantiated type names inherit it.
func reflectPackagePath(packagePath string) string {
	lastSlash := strings.LastIndex(packagePath, "/")
	var escaped strings.Builder
	for index := 0; index < len(packagePath); index++ {
		character := packagePath[index]
		if character <= ' ' || (character == '.' && index > lastSlash) || character == '%' || character == '"' || character >= 0x7F {
			fmt.Fprintf(&escaped, "%%%02x", character)
			continue
		}
		escaped.WriteByte(character)
	}
	return escaped.String()
}
