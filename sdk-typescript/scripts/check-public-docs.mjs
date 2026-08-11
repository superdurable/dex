// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import ts from "typescript";

const configPath = ts.findConfigFile(".", ts.sys.fileExists, "tsconfig.json");
if (configPath === undefined) {
  throw new Error("tsconfig.json not found");
}
const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
if (configFile.error !== undefined) {
  throw new Error(ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n"));
}
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, ".");
const program = ts.createProgram(parsed.fileNames, parsed.options);
const checker = program.getTypeChecker();
const entrypoint = program.getSourceFile("src/index.ts");
if (entrypoint === undefined) {
  throw new Error("src/index.ts not found in the TypeScript program");
}
const entrypointSymbol = checker.getSymbolAtLocation(entrypoint);
if (entrypointSymbol === undefined) {
  throw new Error("cannot resolve the TypeScript SDK entrypoint");
}

const issues = [];
const visited = new Set();
for (const exported of checker.getExportsOfModule(entrypointSymbol)) {
  const symbol = exported.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(exported) : exported;
  for (const declaration of symbol.declarations ?? []) {
    inspectDeclaration(declaration, symbol.name);
  }
}
issues.sort((left, right) => left.file.localeCompare(right.file) || left.line - right.line);
for (const issue of issues) {
  process.stderr.write(`${issue.file}:${issue.line}: ${issue.message}\n`);
}
if (issues.length > 0) {
  process.stderr.write(`public API documentation check failed with ${issues.length} issue(s)\n`);
  process.exitCode = 1;
} else {
  process.stdout.write("all hand-written TypeScript SDK public APIs are documented\n");
}

function inspectDeclaration(node, fallbackName) {
  if (node.getSourceFile().fileName.includes("/gen/") || node.getSourceFile().isDeclarationFile) {
    return;
  }
  const key = `${node.getSourceFile().fileName}:${node.pos}:${node.end}`;
  if (visited.has(key)) {
    return;
  }
  visited.add(key);

  if (ts.isClassDeclaration(node) || ts.isInterfaceDeclaration(node)) {
    const name = node.name?.text ?? fallbackName;
    requireDoc(node, `${ts.isClassDeclaration(node) ? "class" : "interface"} ${name}`, typeParameters(node));
    for (const member of node.members) {
      if (isPublicMember(member)) {
        inspectMember(member, name);
      }
    }
    return;
  }
  if (ts.isTypeAliasDeclaration(node)) {
    requireDoc(node, `type ${node.name.text}`, typeParameters(node));
    inspectTypeNode(node.type, `type ${node.name.text}`);
    return;
  }
  if (ts.isFunctionDeclaration(node)) {
    inspectCallable(node, `function ${node.name?.text ?? fallbackName}`);
    return;
  }
  if (ts.isVariableDeclaration(node)) {
    const name = ts.isIdentifier(node.name) ? node.name.text : fallbackName;
    const callable = callableInitializer(node.initializer);
    if (callable !== undefined) {
      inspectCallable(callable, `constant function ${name}`, documentationNode(node));
    } else {
      requireDoc(documentationNode(node), `constant ${name}`, []);
      if (node.type === undefined) {
        inspectObjectInitializer(node.initializer, `constant ${name}`);
      }
    }
    if (node.type !== undefined) {
      inspectTypeNode(node.type, `constant ${name}`);
    }
  }
}

function inspectMember(member, owner) {
  const name = memberName(member.name) ?? "constructor";
  const label = `${owner}.${name}`;
  if (
    ts.isMethodDeclaration(member) ||
    ts.isMethodSignature(member) ||
    ts.isConstructorDeclaration(member) ||
    ts.isGetAccessorDeclaration(member) ||
    ts.isSetAccessorDeclaration(member) ||
    ts.isCallSignatureDeclaration(member)
  ) {
    inspectCallable(member, label);
    return;
  }
  if (ts.isPropertyDeclaration(member) || ts.isPropertySignature(member)) {
    requireDoc(member, `property ${label}`, typeParameters(member));
    if (member.type !== undefined) {
      inspectTypeNode(member.type, `property ${label}`);
    }
    return;
  }
  if (ts.isConstructSignatureDeclaration(member)) {
    inspectCallable(member, `${owner}.new`);
  }
}

function inspectCallable(callable, label, docNode = callable) {
  const docs = requireDoc(docNode, label, typeParameters(callable));
  if (docs === undefined) {
    return;
  }
  for (const parameter of callable.parameters ?? []) {
    if (ts.isIdentifier(parameter.name) && !hasTag(docs, "param", parameter.name.text)) {
      report(docNode, `${label} must document parameter ${parameter.name.text} with @param`);
    }
    if (parameter.type !== undefined) {
      inspectTypeNode(parameter.type, `${label} parameter ${parameter.name.getText()}`);
    }
  }
  if (returnsValue(callable) && !hasTag(docs, "returns")) {
    report(docNode, `${label} must document its output with @returns`);
  }
}

function inspectTypeNode(typeNode, owner) {
  if (ts.isTypeLiteralNode(typeNode)) {
    for (const member of typeNode.members) {
      inspectMember(member, owner);
    }
    return;
  }
  if (ts.isUnionTypeNode(typeNode) || ts.isIntersectionTypeNode(typeNode)) {
    for (const type of typeNode.types) {
      inspectTypeNode(type, owner);
    }
    return;
  }
  if (ts.isParenthesizedTypeNode(typeNode)) {
    inspectTypeNode(typeNode.type, owner);
    return;
  }
  if (ts.isTypeOperatorNode(typeNode)) {
    inspectTypeNode(typeNode.type, owner);
    return;
  }
  if (ts.isTypeReferenceNode(typeNode)) {
    for (const argument of typeNode.typeArguments ?? []) {
      inspectTypeNode(argument, owner);
    }
  }
}

function inspectObjectInitializer(initializer, owner) {
  let object = unwrapExpression(initializer);
  if (ts.isCallExpression(object) && object.arguments.length > 0) {
    object = unwrapExpression(object.arguments[0]);
  }
  if (!ts.isObjectLiteralExpression(object)) {
    return;
  }
  for (const property of object.properties) {
    if (ts.isSpreadAssignment(property)) {
      continue;
    }
    const name = memberName(property.name) ?? "member";
    if (ts.isMethodDeclaration(property)) {
      inspectCallable(property, `${owner}.${name}`);
    } else {
      requireDoc(property, `property ${owner}.${name}`, []);
    }
  }
}

function unwrapExpression(expression) {
  let current = expression;
  while (
    current !== undefined &&
    (ts.isAsExpression(current) ||
      ts.isTypeAssertionExpression(current) ||
      ts.isSatisfiesExpression(current) ||
      ts.isParenthesizedExpression(current))
  ) {
    current = current.expression;
  }
  return current;
}

function requireDoc(node, label, parameters) {
  const docs = jsDoc(node);
  if (docs === undefined) {
    report(node, `exported ${label} has no JSDoc comment`);
    return undefined;
  }
  const summary = docs.description.split(/\r?\n/, 1)[0].trim();
  if (!/[.!?]$/.test(summary)) {
    report(node, `${label} summary must end with punctuation`);
  }
  for (const parameter of parameters) {
    if (parameter.name !== undefined && !hasTag(docs, "typeParam", parameter.name.text)) {
      report(node, `${label} must document type parameter ${parameter.name.text} with @typeParam`);
    }
  }
  return docs;
}

function jsDoc(node) {
  const sourceFile = node.getSourceFile();
  const ranges = ts.getLeadingCommentRanges(sourceFile.text, node.getFullStart()) ?? [];
  const range = [...ranges].reverse().find((candidate) =>
    sourceFile.text.slice(candidate.pos, candidate.pos + 3) === "/**",
  );
  if (range === undefined) {
    return undefined;
  }
  const raw = sourceFile.text.slice(range.pos + 3, range.end - 2);
  const lines = raw.split(/\r?\n/).map((line) => line.replace(/^\s*\* ?/, ""));
  const description = lines.filter((line) => !line.trimStart().startsWith("@")).join("\n").trim();
  const tags = [];
  for (const line of lines) {
    const match = /^@(\w+)\s*(?:\{[^}]*\}\s*)?([^\s-]+)?\s*(?:-\s*)?(.*)$/.exec(line.trim());
    if (match !== null) {
      tags.push({ name: match[1], target: match[2], description: match[3] });
    }
  }
  return { description, tags };
}

function hasTag(docs, name, target) {
  return docs.tags.some((tag) =>
    tag.name === name &&
    (target === undefined || tag.target === target || tag.target === `<${target}>`) &&
    tag.description.trim().length > 0,
  );
}

function returnsValue(callable) {
  if (ts.isConstructorDeclaration(callable) || ts.isConstructSignatureDeclaration(callable)) {
    return false;
  }
  const type = callable.type;
  if (type === undefined) {
    return false;
  }
  const text = type.getText();
  return text !== "void" && text !== "never" && text !== "Promise<void>";
}

function typeParameters(node) {
  return [...(node.typeParameters ?? [])];
}

function callableInitializer(initializer) {
  if (initializer !== undefined && (ts.isArrowFunction(initializer) || ts.isFunctionExpression(initializer))) {
    return initializer;
  }
  return undefined;
}

function documentationNode(node) {
  if (ts.isVariableDeclaration(node) && ts.isVariableDeclarationList(node.parent)) {
    return node.parent.parent;
  }
  return node;
}

function isPublicMember(member) {
  return !hasModifier(member, ts.SyntaxKind.PrivateKeyword) && !hasModifier(member, ts.SyntaxKind.ProtectedKeyword);
}

function hasModifier(node, kind) {
  return node.modifiers?.some((modifier) => modifier.kind === kind) ?? false;
}

function memberName(name) {
  if (name === undefined) {
    return undefined;
  }
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteral(name)) {
    return name.text;
  }
  return name.getText();
}

function report(node, message) {
  const sourceFile = node.getSourceFile();
  const position = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
  issues.push({ file: sourceFile.fileName, line: position.line + 1, message });
}
