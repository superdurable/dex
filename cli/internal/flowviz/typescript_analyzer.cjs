// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");

class TypeScriptAnalyzer {
  constructor(request) {
    this.path = request.path;
    this.source = request.source;
    const projectRequire = createRequire(this.path);
    try {
      this.ts = require(projectRequire.resolve("typescript"));
    } catch {
      try { this.ts = require("typescript"); }
      catch { throw new Error("TypeScript analysis requires Node.js and the typescript package installed in the source project"); }
    }
    this.tree = this.ts.createSourceFile(this.path, this.source, this.ts.ScriptTarget.Latest, true);
    this.graph = {schemaVersion:"1.0", valid:true, source:{language:"typescript",path:this.path}, flow:{name:""}, nodes:[], edges:[], diagnostics:[]};
    this.imports = new Map();
    this.factories = new Map();
    this.classes = new Map();
    this.values = new Map();
    this.resources = new Map();
    this.registered = new Map();
    this.connectorSteps = new Map();
    this.annotations = [];
    this.uses = [];
  }

  analyze() {
    for (const diagnostic of this.tree.parseDiagnostics) {
      this.error("typescript_parse_failed", this.ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"));
    }
    if (!this.graph.valid) return this.result();
    for (const statement of this.tree.statements) {
      if (this.ts.isImportDeclaration(statement)) this.indexImport(statement);
      if (this.ts.isClassDeclaration(statement) && statement.name) this.classes.set(statement.name.text, statement);
      if (this.ts.isVariableStatement(statement)) for (const declaration of statement.declarationList.declarations) this.indexValue(declaration.name, declaration.initializer);
    }
    const flows = [...this.classes.values()].filter(declaration => this.method(declaration,"getSteps"));
    if (flows.length !== 1) {
      this.error(flows.length ? "multiple_flows" : "flow_not_found", "source must define exactly one Flow");
      return this.result();
    }
    const flow = flows[0];
    this.graph.flow = {name:this.typeName(flow,"getFlowType"),span:this.span(flow)};
    for (const member of flow.members) {
      if (this.ts.isPropertyDeclaration(member)) this.indexValue(member.name,member.initializer,"this.");
      if (this.ts.isConstructorDeclaration(member) && member.body) {
        this.walk(member.body,node => {
          if (this.ts.isVariableDeclaration(node)) this.indexValue(node.name,node.initializer);
          if (this.ts.isBinaryExpression(node) && node.operatorToken.kind === this.ts.SyntaxKind.EqualsToken) this.indexValue(node.left,node.right);
        });
      }
    }
    for (const [name,expression] of this.values) this.indexResource(name,expression);
    const steps = this.method(flow,"getSteps");
    let starts = 0;
    this.walk(steps.body,node => {
      if (!this.ts.isCallExpression(node) || !this.ts.isPropertyAccessExpression(node.expression)) return;
      const methodName = node.expression.name.text;
      if (!["startStep","otherSteps"].includes(methodName)) return;
      for (const [index,argument] of node.arguments.entries()) {
        const isStart = methodName === "startStep" && index === 0;
        const target = this.stepTarget(argument);
        if (!target) { this.error("dynamic_step_registration","registered Step type must be static",argument); continue; }
        if (this.registered.has(target)) continue;
        this.registered.set(target,`step:${target}`);
        const connector = this.connectorSteps.get(target);
        const declaration = this.classes.get(target);
        const name = connector ? target : this.typeName(declaration,"getStepType");
        this.graph.nodes.push({id:`step:${target}`,kind:"step",name,phase:declaration && this.method(declaration,"waitFor") ? "wait_for+execute":"execute",start:isStart,span:this.span(argument)});
        if (isStart) { starts++; this.graph.flow.startStepId = `step:${target}`; }
      }
    });
    if (starts > 1) this.error("multiple_start_steps","Flow defines more than one start Step",steps);
    if (!this.registered.size && !steps.getText(this.tree).includes("StepList.empty")) this.error("dynamic_step_registration","getSteps must directly call StepList.startStep or otherSteps",steps);
    for (const [stepName,nodeID] of this.registered) {
      const connector = this.connectorSteps.get(stepName);
      if (connector) { this.uses.push(this.factoryResult(nodeID,connector)); continue; }
      const declaration = this.classes.get(stepName);
      this.annotations.push({stepId:nodeID,directives:this.directives(declaration),span:this.span(declaration)});
      const execute = this.method(declaration,"execute");
      if (!execute) this.error("step_handler_not_in_file",`Step ${stepName} execute must be defined in the source file`,declaration);
      else this.analyzeDecisions(nodeID,execute.body);
      if (this.method(declaration,"waitFor")) this.error("typescript_wait_for_unsupported","TypeScript WaitFor analysis is not supported yet",this.method(declaration,"waitFor"));
    }
    const bindings = this.method(flow,"getConnectorTriggerBindings");
    if (bindings) {
      const statements = bindings.body?.statements;
      const returned = statements?.length === 1 && this.ts.isReturnStatement(statements[0]) ? this.unwrap(statements[0].expression) : undefined;
      if (!returned || !this.ts.isArrayLiteralExpression(returned)) this.error("connector_trigger_bindings_dynamic","Connector Trigger bindings must be returned as one static array",bindings);
      else for (const expression of returned.elements) {
        const use = this.factoryUse(expression);
        if (use && use.factory.kind === "trigger") this.uses.push(this.factoryResult("",use));
        else this.error("connector_trigger_binding_invalid","Connector Trigger binding must call a manifest factory",expression);
      }
    }
    return this.result();
  }

  indexImport(statement) {
    const moduleName = statement.moduleSpecifier.text;
    const clause = statement.importClause;
    if (!clause?.namedBindings) return;
    const bindings = clause.namedBindings;
    if (moduleName === "@superdurable/dex") {
      if (this.ts.isNamedImports(bindings)) for (const element of bindings.elements) this.imports.set(element.name.text,element.propertyName?.text || element.name.text);
      else this.imports.set(bindings.name.text,"*");
      return;
    }
    if (moduleName.startsWith(".") || moduleName.startsWith("/")) return;
    const components = moduleName.split("/");
    const packageName = moduleName.startsWith("@") ? components.slice(0,2).join("/") : components[0];
    for (let directory = path.dirname(this.path);; directory = path.dirname(directory)) {
      const root = path.join(directory,"node_modules",packageName);
      if (fs.existsSync(path.join(root,"package.json"))) {
        const manifestPath = path.join(root,"dex-connector.json");
        if (!fs.existsSync(manifestPath)) return;
        try {
          const manifest = JSON.parse(fs.readFileSync(manifestPath,"utf8"));
          for (const factory of manifest.factories || []) {
            if (this.ts.isNamedImports(bindings)) {
              for (const element of bindings.elements) if ((element.propertyName?.text || element.name.text) === factory.name) this.factories.set(element.name.text,{manifestPath,factory});
            } else this.factories.set(`${bindings.name.text}.${factory.name}`,{manifestPath,factory});
          }
        } catch (error) { this.error("connector_manifest",`cannot read Connector manifest: ${error.message}`,statement); }
        return;
      }
      if (path.dirname(directory) === directory) return;
    }
  }

  indexValue(name,expression,prefix="") {
    if (name && expression) this.values.set(prefix+name.getText(this.tree),expression);
  }

  indexResource(name,expression) {
    expression = this.unwrap(expression);
    if (!expression || !this.ts.isNewExpression(expression)) return;
    const constructor = this.dexName(expression.expression);
    const kind = {Attribute:"attribute",AttributeMap:"attribute",Channel:"channel",ChannelMap:"channel",Stream:"stream"}[constructor];
    if (!kind) return;
    const resourceName = this.staticLiteral(expression.arguments?.[0]);
    if (typeof resourceName !== "string") { this.error("dynamic_resource_name",`resource ${name} must use a static name`,expression); return; }
    const nodeID = `resource:${kind}:${name}`;
    this.resources.set(name,nodeID);
    const valueType = expression.typeArguments?.[0]?.getText(this.tree) || "unknown";
    this.graph.nodes.push({id:nodeID,kind,name:resourceName,span:this.span(expression),resource:{valueType,map:constructor.endsWith("Map")}});
  }

  factoryUse(expression) {
    expression = this.resolveValue(expression);
    if (!expression || !this.ts.isCallExpression(expression)) return undefined;
    const identity = this.factories.get(expression.expression.getText(this.tree));
    if (!identity) return undefined;
    const config = this.unwrap(expression.arguments[0]);
    if (expression.arguments.length !== 1 || !config || !this.ts.isObjectLiteralExpression(config)) {
      this.error("connector_factory_config","Connector factory requires one inline config object",expression);
      return undefined;
    }
    const fields = new Map();
    for (const property of config.properties) {
      if (this.ts.isPropertyAssignment(property)) {
        const name = this.propertyName(property.name);
        if (fields.has(name)) this.error("connector_factory_config","Connector config cannot duplicate fields",property);
        fields.set(name,property.initializer);
      } else if (this.ts.isShorthandPropertyAssignment(property)) fields.set(property.name.text,property.name);
      else this.error("connector_factory_config","Connector config cannot use spreads or methods",property);
    }
    return {...identity,fields,expression};
  }

  stepTarget(expression) {
    expression = this.resolveValue(expression);
    if (!expression) return undefined;
    const use = this.factoryUse(expression);
    if (use && use.factory.kind !== "trigger") {
      const stepType = this.staticLiteral(use.fields.get(use.factory.fields?.stepType));
      if (typeof stepType !== "string" || !stepType) { this.error("connector_factory_step_type","Connector factory requires a non-empty static Step type",expression); return undefined; }
      this.connectorSteps.set(stepType,use);
      return stepType;
    }
    if (this.ts.isNewExpression(expression) && this.classes.has(expression.expression.getText(this.tree))) return expression.expression.getText(this.tree);
    if (this.ts.isIdentifier(expression) && this.classes.has(expression.text)) return expression.text;
    return undefined;
  }

  analyzeDecisions(nodeID,body,condition="") {
    if (!body) return;
    let hasDecision = false;
    for (const statement of body.statements || []) {
      if (this.ts.isIfStatement(statement)) {
        const nested = this.ts.isBlock(statement.thenStatement) ? statement.thenStatement : {statements:[statement.thenStatement]};
        this.analyzeDecisions(nodeID,nested,statement.expression.getText(this.tree));
        if (statement.elseStatement) this.analyzeDecisions(nodeID,this.ts.isBlock(statement.elseStatement) ? statement.elseStatement : {statements:[statement.elseStatement]},`!(${statement.expression.getText(this.tree)})`);
        hasDecision = true;
      }
      if (!this.ts.isReturnStatement(statement)) continue;
      const expression = this.unwrap(statement.expression);
      if (!expression || !this.ts.isCallExpression(expression) || !this.ts.isPropertyAccessExpression(expression.expression) || this.dexName(expression.expression.expression) !== "StepDecision") {
        this.error("hidden_dex_decision","execute must return a static StepDecision",statement); continue;
      }
      const decision = expression.expression.name.text;
      hasDecision = true;
      if (["goTo","goToMany"].includes(decision)) {
        for (const argument of expression.arguments) {
          const target = this.stepTarget(argument);
          if (!this.registered.has(target)) { this.error("unknown_step_target","Dex transition target must be a registered static Step",argument); continue; }
          this.graph.edges.push({kind:"transition",from:nodeID,to:this.registered.get(target),condition,span:this.span(expression)});
        }
      } else if (["forceComplete","forceFail","gracefulComplete","deadEnd"].includes(decision)) {
        this.graph.nodes.push({id:`decision:${nodeID}:${statement.pos}`,kind:"decision",name:decision,parentId:nodeID,condition,phase:"execute",span:this.span(expression),decision:{type:decision}});
      } else this.error("dynamic_dex_decision",`unsupported StepDecision.${decision}`,expression);
    }
    if (!hasDecision) this.error("hidden_dex_decision","execute must return a static StepDecision",body);
  }

  factoryResult(nodeID,use) {
    return {stepId:nodeID,manifestPath:use.manifestPath,factoryName:use.factory.name,fields:Object.fromEntries([...use.fields].map(([name,value]) => [name,this.scriptValue(value)])),span:this.span(use.expression)};
  }

  scriptValue(expression) {
    const literal = this.staticLiteral(expression);
    if (literal !== undefined) return {literal};
    const name = expression?.getText(this.tree);
    if (this.resources.has(name)) return {resource:this.resources.get(name)};
    const target = this.stepTarget(expression);
    if (target) return {target:`step:${target}`};
    expression = this.unwrap(expression);
    if (expression && this.ts.isCallExpression(expression)) {
      const name = expression.expression.getText(this.tree).split(".").at(-1);
      if (name === "stepRef" && expression.arguments.length === 1) {
        const typeName = this.staticLiteral(expression.arguments[0]);
        const node = this.graph.nodes.find(node => node.kind === "step" && node.name === typeName);
        if (node) return {target:node.id};
      }
      if (name === "goTo") return {call:name,arguments:expression.arguments.map(argument => this.scriptValue(argument))};
    }
    return {};
  }

  staticLiteral(expression,seen=new Set()) {
    expression = this.unwrap(expression);
    if (!expression) return undefined;
    if (this.ts.isStringLiteral(expression) || this.ts.isNoSubstitutionTemplateLiteral(expression)) return expression.text;
    if (this.ts.isNumericLiteral(expression)) return Number(expression.text);
    if (expression.kind === this.ts.SyntaxKind.TrueKeyword) return true;
    if (expression.kind === this.ts.SyntaxKind.FalseKeyword) return false;
    if (expression.kind === this.ts.SyntaxKind.NullKeyword) return null;
    const name = expression.getText(this.tree);
    if (this.values.has(name) && !seen.has(name)) { const next = new Set(seen); next.add(name); return this.staticLiteral(this.values.get(name),next); }
    if (this.ts.isArrayLiteralExpression(expression)) {
      const values = expression.elements.map(element => this.staticLiteral(element,seen));
      return values.some(value => value === undefined) ? undefined : values;
    }
    if (this.ts.isObjectLiteralExpression(expression)) {
      const object = {};
      for (const property of expression.properties) {
        if (!this.ts.isPropertyAssignment(property)) return undefined;
        const value = this.staticLiteral(property.initializer,seen);
        if (value === undefined) return undefined;
        object[this.propertyName(property.name)] = value;
      }
      return object;
    }
    return undefined;
  }

  directives(declaration) {
    let line = this.tree.getLineAndCharacterOfPosition(declaration.getStart(this.tree)).line;
    const lines = this.source.split(/\r?\n/);
    const directives = [];
    while (line > 0 && lines[line-1].trimStart().startsWith("//")) {
      const text = lines[--line].trimStart().slice(2).trim();
      if (text.startsWith("dex:")) directives.unshift(text);
    }
    return directives;
  }

  resolveValue(expression) {
    const seen = new Set();
    expression = this.unwrap(expression);
    while (expression && this.values.has(expression.getText(this.tree)) && !seen.has(expression.getText(this.tree))) {
      const name = expression.getText(this.tree); seen.add(name); expression = this.unwrap(this.values.get(name));
    }
    return expression;
  }

  typeName(declaration,methodName) {
    const method = this.method(declaration,methodName);
    if (!method) return declaration.name.text;
    const returns = [];
    this.walk(method.body,node => { if (this.ts.isReturnStatement(node)) returns.push(this.staticLiteral(node.expression)); });
    if (!returns.length || returns.some(value => typeof value !== "string" || value !== returns[0])) this.error("dynamic_type_name",`${methodName} must return one static string`,method);
    return returns[0] || declaration.name.text;
  }

  dexName(expression) {
    if (this.ts.isIdentifier(expression)) return this.imports.get(expression.text);
    if (this.ts.isPropertyAccessExpression(expression) && this.imports.get(expression.expression.getText(this.tree)) === "*") return expression.name.text;
    return undefined;
  }

  unwrap(expression) {
    while (expression && (this.ts.isParenthesizedExpression(expression) || this.ts.isAsExpression(expression) || this.ts.isSatisfiesExpression(expression) || this.ts.isNonNullExpression(expression))) expression = expression.expression;
    return expression;
  }

  method(declaration,name) { return declaration?.members.find(member => this.ts.isMethodDeclaration(member) && this.propertyName(member.name) === name); }
  propertyName(name) { return this.ts.isStringLiteral(name) ? name.text : name.getText(this.tree); }
  walk(node,visit) { if (!node) return; visit(node); this.ts.forEachChild(node,child => this.walk(child,visit)); }
  span(node) {
    const start = this.tree.getLineAndCharacterOfPosition(node.getStart(this.tree));
    const end = this.tree.getLineAndCharacterOfPosition(node.end);
    return {startLine:start.line+1,startColumn:start.character+1,endLine:end.line+1,endColumn:end.character+1};
  }
  error(code,message,node) { this.graph.valid = false; this.graph.diagnostics.push({severity:"error",code,message,...(node ? {span:this.span(node)} : {})}); }
  result() { return {graph:this.graph,annotations:this.annotations,factories:this.uses}; }
}

try {
  process.stdout.write(JSON.stringify(new TypeScriptAnalyzer(JSON.parse(fs.readFileSync(0,"utf8"))).analyze()));
} catch (error) {
  process.stderr.write(error.message);
  process.exitCode = 1;
}
