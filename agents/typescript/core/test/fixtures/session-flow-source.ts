// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import ts from "typescript";

/** agents/typescript/core/src, from dist/test/fixtures. */
const sourceDirectory = fileURLToPath(new URL("../../../src/", import.meta.url));

/** Picks a tool Step type by effect. A movement through it can reach every Step type it returns. */
const stepTypeChoiceFunction = "toolStepTypeForEffect";

/** Step handlers and option builders: a Step's route is not reached through them by name. */
const unfollowedNames = new Set([
  "execute",
  "waitFor",
  "getStepOptions",
  "getStepType",
  "toolStepOptions",
  "toolTaskPollOptions",
  stepTypeChoiceFunction,
]);
/** Calls whose Step type arguments are failure routes, never movements. */
const failureRouteCalls = new Set(["ExecuteFailure.proceedTo", "toolStepOptions", "toolTaskPollOptions"]);
const channelConditionMethods = new Set(["forOne", "forN", "atLeast", "atMost", "range"]);

export interface ScannedWait {
  readonly waitTypes: ReadonlySet<string>;
  /** The persistence field of each Channel, and `Timer` for a Timer. */
  readonly conditions: ReadonlySet<string>;
}

type FunctionNode = ts.FunctionLikeDeclaration;

/**
 * Reads the waits and routes of the session Steps from the TypeScript source.
 *
 * A Step's wait conditions depend on state and its movements run through helpers, so neither can be read off the
 * registered Flow. Names resolve without types, so two helpers that share a name both count.
 */
export class SessionFlowSource {
  private readonly classes = new Map<string, ts.ClassLikeDeclaration>();
  private readonly functions = new Map<string, FunctionNode[]>();
  private readonly choiceFunctions = new Map<string, FunctionNode[]>();

  public constructor() {
    for (const fileName of readdirSync(sourceDirectory).filter((name) => /^agent-session-.*\.ts$/.test(name))) {
      const text = readFileSync(`${sourceDirectory}${fileName}`, "utf8");
      this.index(ts.createSourceFile(fileName, text, ts.ScriptTarget.ES2022, true));
    }
  }

  /** Returns the Step types the `execute` of one class moves to, and the endings it can return. */
  public scanExecute(className: string): { targets: Set<string>; endings: Set<string> } {
    const targets = new Set<string>();
    const endings = new Set<string>();
    for (const root of this.reachable(this.method(className, "execute"))) {
      visitDescendants(root, (node) => {
        if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
          if (node.expression.text === "deadEnd" || node.expression.text === "forceFail") {
            endings.add(node.expression.text);
          }
          if (node.expression.text === stepTypeChoiceFunction && isRouteReference(node)) {
            for (const key of this.stepTypeKeysIn(stepTypeChoiceFunction)) {
              targets.add(key);
            }
          }
        }
        const stepTypeKey = stepTypeKeyOf(node);
        if (stepTypeKey !== undefined && isRouteReference(node)) {
          targets.add(stepTypeKey);
        }
      });
    }
    return { targets, endings };
  }

  /** Returns what the `waitFor` of one class can wait on. */
  public scanWait(className: string): ScannedWait {
    const waitTypes = new Set<string>();
    const conditions = new Set<string>();
    for (const root of this.reachable(this.method(className, "waitFor"))) {
      visitDescendants(root, (node) => {
        if (!ts.isCallExpression(node) || !ts.isPropertyAccessExpression(node.expression)) {
          return;
        }
        const method = node.expression.name.text;
        const receiver = node.expression.expression;
        if (ts.isIdentifier(receiver) && receiver.text === "Wait") {
          waitTypes.add(method);
        } else if (ts.isIdentifier(receiver) && receiver.text === "Timer") {
          conditions.add("Timer");
        } else if (channelConditionMethods.has(method) && ts.isPropertyAccessExpression(receiver)) {
          conditions.add(receiver.name.text);
        }
      });
    }
    return { waitTypes, conditions };
  }

  /** Returns the persistence field of each Channel that `execute` of one class, or an RPC method, publishes to. */
  public scanPublishes(className: string, methodName: string): Set<string> {
    const channels = new Set<string>();
    for (const root of this.reachable(this.method(className, methodName))) {
      visitDescendants(root, (node) => {
        if (
          ts.isCallExpression(node) &&
          ts.isPropertyAccessExpression(node.expression) &&
          node.expression.name.text === "publish" &&
          ts.isPropertyAccessExpression(node.expression.expression)
        ) {
          channels.add(node.expression.expression.name.text);
        }
      });
    }
    return channels;
  }

  /** Returns the keys of the Step types that an RPC lists in `cancelingSteps`. */
  public scanCancelingSteps(): Set<string> {
    const keys = new Set<string>();
    for (const classNode of this.classes.values()) {
      visitDescendants(classNode, (node) => {
        if (ts.isPropertyAssignment(node) && node.name.getText() === "cancelingSteps") {
          visitDescendants(node, (descendant) => {
            const key = stepTypeKeyOf(descendant);
            if (key !== undefined) {
              keys.add(key);
            }
          });
        }
      });
    }
    return keys;
  }

  private stepTypeKeysIn(functionName: string): Set<string> {
    const keys = new Set<string>();
    const [declaration] = this.choiceFunctions.get(functionName) ?? [];
    if (declaration === undefined) {
      throw new Error(`${functionName} is not in the session source`);
    }
    visitDescendants(declaration, (node) => {
      const key = stepTypeKeyOf(node);
      if (key !== undefined) {
        keys.add(key);
      }
    });
    return keys;
  }

  /** Returns whether a class defines `waitFor`. */
  public hasWaitFor(className: string): boolean {
    return this.findMethod(className, "waitFor") !== undefined;
  }

  private index(sourceFile: ts.SourceFile): void {
    visitDescendants(sourceFile, (node) => {
      if ((ts.isClassDeclaration(node) || ts.isClassExpression(node)) && node.name !== undefined) {
        this.classes.set(node.name.text, node);
      }
      if ((ts.isMethodDeclaration(node) || ts.isFunctionDeclaration(node)) && node.name !== undefined) {
        const name = node.name.getText(sourceFile);
        if (name === stepTypeChoiceFunction) {
          this.choiceFunctions.set(name, [node]);
        } else if (!unfollowedNames.has(name) && node.body !== undefined) {
          this.functions.set(name, [...(this.functions.get(name) ?? []), node]);
        }
      }
    });
  }

  private findMethod(className: string, methodName: string): ts.MethodDeclaration | undefined {
    return this.classes.get(className)?.members.find(
      (member): member is ts.MethodDeclaration =>
        ts.isMethodDeclaration(member) && member.name.getText() === methodName,
    );
  }

  private method(className: string, methodName: string): ts.MethodDeclaration {
    const method = this.findMethod(className, methodName);
    if (method === undefined) {
      throw new Error(`${className}.${methodName} is not in the session source`);
    }
    return method;
  }

  private reachable(root: FunctionNode): FunctionNode[] {
    const reached = new Set<FunctionNode>([root]);
    for (const node of reached) {
      visitDescendants(node, (descendant) => {
        if (ts.isIdentifier(descendant)) {
          for (const next of this.functions.get(descendant.text) ?? []) {
            reached.add(next);
          }
        }
      });
    }
    return [...reached];
  }
}

function visitDescendants(root: ts.Node, visit: (node: ts.Node) => void): void {
  ts.forEachChild(root, (child) => {
    visit(child);
    visitDescendants(child, visit);
  });
}

function stepTypeKeyOf(node: ts.Node): string | undefined {
  return ts.isPropertyAccessExpression(node) && node.expression.getText() === "agentSessionStepTypes"
    ? node.name.text
    : undefined;
}

/** Whether a Step type names a movement, rather than a failure route, a cancellation, or a comparison. */
function isRouteReference(reference: ts.Node): boolean {
  let child: ts.Node = reference;
  for (let node = reference.parent; node !== undefined; child = node, node = node.parent) {
    if (ts.isFunctionLike(node)) {
      return true;
    }
    if (ts.isCaseClause(node) && node.expression === child) {
      return false;
    }
    if (ts.isBinaryExpression(node) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(node.operatorToken.kind)) {
      return false;
    }
    if (ts.isCallExpression(node) && failureRouteCalls.has(node.expression.getText())) {
      return false;
    }
    if (ts.isVariableDeclaration(node) && node.initializer === child && isFailureRouteOnly(node)) {
      return false;
    }
    if (ts.isPropertyAssignment(node) && node.name.getText() === "cancelingSteps") {
      return false;
    }
  }
  return true;
}

/** Whether a variable only feeds failure-route options, like the recovery Step of a movement. */
function isFailureRouteOnly(declaration: ts.VariableDeclaration): boolean {
  if (!ts.isIdentifier(declaration.name)) {
    return false;
  }
  const name = declaration.name.text;
  let scope: ts.Node = declaration;
  while (!ts.isFunctionLike(scope) && scope.parent !== undefined) {
    scope = scope.parent;
  }
  let isUsed = false;
  let isOnlyFailureRoute = true;
  visitDescendants(scope, (node) => {
    if (!ts.isIdentifier(node) || node.text !== name || node === declaration.name) {
      return;
    }
    isUsed = true;
    const call = node.parent;
    if (!ts.isCallExpression(call) || !failureRouteCalls.has(call.expression.getText())) {
      isOnlyFailureRoute = false;
    }
  });
  return isUsed && isOnlyFailureRoute;
}
