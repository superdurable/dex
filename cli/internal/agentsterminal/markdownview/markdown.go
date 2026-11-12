// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package markdownview renders assistant markdown for the agents terminal.
// Code is not highlighted.
package markdownview

import (
	"strconv"
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// RenderMarkdown returns styled lines of at most width cells. Prose wraps;
// code lines are truncated.
func RenderMarkdown(source string, width int, theme terminaltheme.Theme) []string {
	sourceBytes := []byte(source)
	document := goldmark.New().Parser().Parse(text.NewReader(sourceBytes))
	renderer := markdownRenderer{source: sourceBytes, theme: theme}
	return renderer.renderBlocks(document, width)
}

type markdownRenderer struct {
	source []byte
	theme  terminaltheme.Theme
}

func (renderer markdownRenderer) renderBlocks(parent ast.Node, width int) []string {
	var lines []string
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		blockLines := renderer.renderBlock(child, width)
		if len(blockLines) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, blockLines...)
	}
	return lines
}

func (renderer markdownRenderer) renderBlock(node ast.Node, width int) []string {
	switch block := node.(type) {
	case *ast.Heading:
		style := renderer.theme.Strong
		if block.Level == 1 {
			style = renderer.theme.Title
		}
		return terminaltheme.WrapToWidth(style.Render(renderer.renderInlines(block)), width)
	case *ast.Paragraph, *ast.TextBlock:
		return terminaltheme.WrapToWidth(renderer.renderInlines(block), width)
	case *ast.List:
		return renderer.renderList(block, width)
	case *ast.Blockquote:
		return renderer.prefixLines(renderer.renderBlocks(block, width-2), renderer.theme.Secondary.Render(renderer.theme.Symbols.Gutter)+" ")
	case *ast.FencedCodeBlock:
		return renderer.renderCode(block.Lines(), string(block.Language(renderer.source)), width)
	case *ast.CodeBlock:
		return renderer.renderCode(block.Lines(), "", width)
	case *ast.ThematicBreak:
		return []string{renderer.theme.Secondary.Render(strings.Repeat(renderer.theme.Symbols.Rule, min(width, 24)))}
	case *ast.HTMLBlock:
		return renderer.renderRawLines(block.Lines(), width)
	default:
		return renderer.renderBlocks(node, width)
	}
}

func (renderer markdownRenderer) renderList(list *ast.List, width int) []string {
	var lines []string
	number := list.Start
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "-"
		if list.IsOrdered() {
			marker = strconv.Itoa(number) + "."
			number++
		}
		markerWidth := terminaltheme.DisplayWidth(marker) + 1
		itemLines := renderer.renderListItem(item, width-markerWidth, list.IsTight)
		if len(itemLines) == 0 {
			itemLines = []string{""}
		}
		if !list.IsTight && len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, marker+" "+itemLines[0])
		indent := strings.Repeat(" ", markerWidth)
		for _, line := range itemLines[1:] {
			if line == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, indent+line)
		}
	}
	return lines
}

func (renderer markdownRenderer) renderListItem(item ast.Node, width int, isTight bool) []string {
	if !isTight {
		return renderer.renderBlocks(item, width)
	}
	var lines []string
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		lines = append(lines, renderer.renderBlock(child, width)...)
	}
	return lines
}

func (renderer markdownRenderer) renderCode(segments *text.Segments, language string, width int) []string {
	gutter := renderer.theme.Secondary.Render(renderer.theme.Symbols.Gutter) + " "
	contentWidth := max(width-2, 1)
	lines := make([]string, 0, segments.Len())
	for index := 0; index < segments.Len(); index++ {
		segment := segments.At(index)
		line := strings.TrimRight(string(segment.Value(renderer.source)), "\r\n")
		line = strings.ReplaceAll(line, "\t", "    ")
		lines = append(lines, gutter+terminaltheme.TruncateToWidth(line, contentWidth, renderer.theme.Symbols.Ellipsis))
	}
	if language == "" {
		return lines
	}
	label := renderer.theme.Secondary.Render(language)
	if len(lines) == 0 {
		return []string{gutter + label}
	}
	firstLineRoom := width - terminaltheme.DisplayWidth(lines[0]) - 1
	if firstLineRoom >= terminaltheme.DisplayWidth(language) {
		lines[0] = terminaltheme.PadRight(lines[0], width-terminaltheme.DisplayWidth(language)) + label
		return lines
	}
	return append([]string{terminaltheme.PadLeft(label, width)}, lines...)
}

func (renderer markdownRenderer) renderRawLines(segments *text.Segments, width int) []string {
	var lines []string
	for index := 0; index < segments.Len(); index++ {
		segment := segments.At(index)
		line := strings.TrimRight(string(segment.Value(renderer.source)), "\r\n")
		lines = append(lines, terminaltheme.WrapToWidth(line, width)...)
	}
	return lines
}

func (renderer markdownRenderer) prefixLines(lines []string, prefix string) []string {
	prefixed := make([]string, 0, len(lines))
	for _, line := range lines {
		prefixed = append(prefixed, prefix+line)
	}
	return prefixed
}

func (renderer markdownRenderer) renderInlines(parent ast.Node) string {
	var builder strings.Builder
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		builder.WriteString(renderer.renderInline(child))
	}
	return builder.String()
}

func (renderer markdownRenderer) renderInline(node ast.Node) string {
	switch inline := node.(type) {
	case *ast.Text:
		value := string(inline.Value(renderer.source))
		if inline.HardLineBreak() {
			return value + "\n"
		}
		if inline.SoftLineBreak() {
			return value + " "
		}
		return value
	case *ast.String:
		return string(inline.Value)
	case *ast.CodeSpan:
		return renderer.theme.Secondary.Render(renderer.plainText(inline))
	case *ast.Emphasis:
		style := renderer.theme.Emphasis
		if inline.Level >= 2 {
			style = renderer.theme.Strong
		}
		return style.Render(renderer.renderInlines(inline))
	case *ast.Link:
		label := renderer.renderInlines(inline)
		destination := string(inline.Destination)
		if destination == "" || ansiStrippedEquals(label, destination) {
			return label
		}
		return label + " " + renderer.theme.Secondary.Render("("+destination+")")
	case *ast.AutoLink:
		return string(inline.URL(renderer.source))
	case *ast.Image:
		return renderer.theme.Secondary.Render("[image: " + renderer.plainText(inline) + "]")
	case *ast.RawHTML:
		var builder strings.Builder
		for index := 0; index < inline.Segments.Len(); index++ {
			segment := inline.Segments.At(index)
			builder.Write(segment.Value(renderer.source))
		}
		return builder.String()
	default:
		return renderer.renderInlines(node)
	}
}

func (renderer markdownRenderer) plainText(parent ast.Node) string {
	var builder strings.Builder
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch inline := child.(type) {
		case *ast.Text:
			builder.Write(inline.Value(renderer.source))
		case *ast.String:
			builder.Write(inline.Value)
		default:
			builder.WriteString(renderer.plainText(child))
		}
	}
	return builder.String()
}

func ansiStrippedEquals(styled string, plain string) bool {
	return strings.TrimSpace(terminaltheme.StripStyles(styled)) == strings.TrimSpace(plain)
}
