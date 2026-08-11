// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type documentationIssue struct {
	position token.Position
	message  string
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"dex", "logging"}
	}
	issues, err := inspectRoots(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "%s: %s\n", issue.position, issue.message)
	}
	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "public API documentation check failed with %d issue(s)\n", len(issues))
		os.Exit(1)
	}
	fmt.Println("all hand-written Go SDK public APIs are documented")
}

func inspectRoots(roots []string) ([]documentationIssue, error) {
	fileSet := token.NewFileSet()
	var issues []documentationIssue
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "gen" || strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			issues = append(issues, inspectFile(fileSet, parsed)...)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", root, err)
		}
	}
	return issues, nil
}

func inspectFile(fileSet *token.FileSet, file *ast.File) []documentationIssue {
	var issues []documentationIssue
	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			if typed.Name.IsExported() && receiverIsExported(typed.Recv) {
				issues = append(issues, requireNamedDoc(fileSet, typed.Pos(), typed.Name.Name, typed.Doc)...)
			}
		case *ast.GenDecl:
			issues = append(issues, inspectGeneralDeclaration(fileSet, typed)...)
		}
	}
	return issues
}

func receiverIsExported(receiver *ast.FieldList) bool {
	if receiver == nil || len(receiver.List) == 0 {
		return true
	}
	return ast.IsExported(embeddedTypeName(receiver.List[0].Type))
}

func inspectGeneralDeclaration(fileSet *token.FileSet, declaration *ast.GenDecl) []documentationIssue {
	var issues []documentationIssue
	for _, specification := range declaration.Specs {
		switch typed := specification.(type) {
		case *ast.TypeSpec:
			if !typed.Name.IsExported() {
				continue
			}
			doc := typed.Doc
			if doc == nil && len(declaration.Specs) == 1 {
				doc = declaration.Doc
			}
			issues = append(issues, requireNamedDoc(fileSet, typed.Pos(), typed.Name.Name, doc)...)
			issues = append(issues, inspectTypeMembers(fileSet, typed)...)
		case *ast.ValueSpec:
			for _, name := range typed.Names {
				if !name.IsExported() {
					continue
				}
				doc := typed.Doc
				if doc == nil {
					doc = typed.Comment
				}
				if doc == nil && len(declaration.Specs) == 1 && len(typed.Names) == 1 {
					doc = declaration.Doc
				}
				issues = append(issues, requireNamedDoc(fileSet, name.Pos(), name.Name, doc)...)
			}
		}
	}
	return issues
}

func inspectTypeMembers(fileSet *token.FileSet, specification *ast.TypeSpec) []documentationIssue {
	var fields *ast.FieldList
	switch typed := specification.Type.(type) {
	case *ast.StructType:
		fields = typed.Fields
	case *ast.InterfaceType:
		fields = typed.Methods
	default:
		return nil
	}
	var issues []documentationIssue
	for _, field := range fields.List {
		for _, name := range exportedFieldNames(field) {
			doc := field.Doc
			if doc == nil {
				doc = field.Comment
			}
			issues = append(issues, requireNamedDoc(fileSet, field.Pos(), name, doc)...)
		}
	}
	return issues
}

func exportedFieldNames(field *ast.Field) []string {
	var names []string
	for _, name := range field.Names {
		if name.IsExported() {
			names = append(names, name.Name)
		}
	}
	if len(field.Names) != 0 {
		return names
	}
	name := embeddedTypeName(field.Type)
	if ast.IsExported(name) {
		return []string{name}
	}
	return nil
}

func embeddedTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.StarExpr:
		return embeddedTypeName(typed.X)
	case *ast.IndexExpr:
		return embeddedTypeName(typed.X)
	case *ast.IndexListExpr:
		return embeddedTypeName(typed.X)
	default:
		return ""
	}
}

func requireNamedDoc(
	fileSet *token.FileSet,
	position token.Pos,
	name string,
	doc *ast.CommentGroup,
) []documentationIssue {
	if doc == nil || strings.TrimSpace(doc.Text()) == "" {
		return []documentationIssue{{
			position: fileSet.Position(position),
			message:  fmt.Sprintf("exported API %s has no doc comment", name),
		}}
	}
	text := strings.TrimSpace(doc.Text())
	if !strings.HasPrefix(text, name) {
		return []documentationIssue{{
			position: fileSet.Position(position),
			message:  fmt.Sprintf("doc comment for %s must begin with its declared name", name),
		}}
	}
	return nil
}
