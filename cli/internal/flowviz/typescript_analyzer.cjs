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

const terminalDecisions = new Set(["gracefulComplete", "forceComplete", "forceFail", "deadEnd", "forceCompleteIfChannelsEmpty"]);
const cancelingDecisions = new Set(["withCancelingSteps", "withCancelingSiblingSteps"]);
const codecValueTypes = {stringCodec:"string", booleanCodec:"boolean", int64Codec:"bigint", doubleCodec:"number", bytesCodec:"Uint8Array", voidCodec:"void"};

class TypeScriptAnalyzer {
  constructor(request) {
    this.path = request.path;
    this.source = request.source;
    let compilerPath;
    try {
      compilerPath = createRequire(this.path).resolve("typescript");
    } catch {
      throw new Error("TypeScript analysis requires Node.js and the typescript package installed in the source project");
    }
    this.ts = require(compilerPath);
    this.tree = this.ts.createSourceFile(this.path, this.source, this.ts.ScriptTarget.Latest, true);
    this.graph = {schemaVersion:"1.0", valid:true, source:{language:"typescript",path:this.path}, flow:{name:""}, nodes:[], edges:[], diagnostics:[]};
    this.imports = new Map();
    this.factories = new Map();
    this.missingFactories = new Map();
    this.classes = new Map();
    this.functions = new Map();
    this.values = new Map();
    this.resources = new Map();
    this.registered = new Map();
    this.stepInstances = new Map();
    this.registeredIdentities = new Map();
    this.resourceNodes = new Map();
    this.targetFailures = new Map();
    this.currentFrame = {};
    this.connectorSteps = new Map();
    this.failureTransitions = new Set();
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
      if (this.ts.isFunctionDeclaration(statement) && statement.name && statement.body) this.functions.set(statement.name.text, statement);
      if (this.ts.isVariableStatement(statement)) for (const declaration of statement.declarationList.declarations) this.indexValue(declaration.name, declaration.initializer);
    }
    this.indexBindings();
    const flows = [...this.classes.values()].filter(declaration => this.method(declaration,"getSteps"));
    if (flows.length !== 1) {
      this.error(flows.length ? "multiple_flows" : "flow_not_found", "source must define exactly one Flow");
      return this.result();
    }
    const flow = flows[0];
    this.flowClass = flow;
    this.indexEscapes();
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
    this.analyzeRegistration(this.method(flow,"getSteps"));
    this.validateClassCustomization();
    for (const [stepName,nodeID] of this.registered) {
      const connector = this.connectorSteps.get(stepName);
      if (connector) { this.uses.push(this.factoryResult(nodeID,connector)); continue; }
      const instance = this.stepInstances.get(stepName);
      this.currentFrame = {instance};
      this.analyzeStep(stepName,nodeID,instance.declaration);
      this.currentFrame = {};
    }
    this.analyzeTriggerBindings(this.method(flow,"getConnectorTriggerBindings"));
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
    if (moduleName.startsWith(".") || moduleName.startsWith("/") || moduleName.startsWith("node:")) return;
    const components = moduleName.split("/");
    const packageName = moduleName.startsWith("@") ? components.slice(0,2).join("/") : components[0];
    const importedNames = this.ts.isNamedImports(bindings) ? bindings.elements.map(element => element.name.text) : [`${bindings.name.text}.`];
    for (let directory = path.dirname(this.path);; directory = path.dirname(directory)) {
      const root = path.join(directory,"node_modules",packageName);
      if (fs.existsSync(path.join(root,"package.json"))) {
        const manifestPath = path.join(root,"dex-connector.json");
        if (!fs.existsSync(manifestPath)) {
          for (const name of importedNames) this.missingFactories.set(name,`package ${packageName} has no Connector manifest at ${manifestPath}`);
          return;
        }
        try {
          const manifest = JSON.parse(fs.readFileSync(manifestPath,"utf8"));
          this.validateManifestShape(manifest);
          for (const factory of manifest.factories) {
            if (this.ts.isNamedImports(bindings)) {
              for (const element of bindings.elements) if ((element.propertyName?.text || element.name.text) === factory.name) this.factories.set(element.name.text,{manifestPath,factory});
            } else this.factories.set(`${bindings.name.text}.${factory.name}`,{manifestPath,factory});
          }
        } catch (error) { this.error("connector_manifest",`cannot read Connector manifest ${manifestPath}: ${error.message}`,statement); }
        return;
      }
      if (path.dirname(directory) === directory) break;
    }
    for (const name of importedNames) this.missingFactories.set(name,`package ${packageName} is not installed in node_modules above ${path.dirname(this.path)}`);
  }

  validateManifestShape(manifest) {
    if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)) throw new Error("manifest must be a JSON object");
    if (!Array.isArray(manifest.factories)) throw new Error("factories must be an array");
    for (const [index,factory] of manifest.factories.entries()) {
      if (!factory || typeof factory !== "object" || Array.isArray(factory)) throw new Error(`factories[${index}] must be an object`);
      if (typeof factory.name !== "string" || !factory.name) throw new Error(`factories[${index}].name must be a non-empty string`);
      if (typeof factory.kind !== "string") throw new Error(`factories[${index}].kind must be a string`);
      if (!factory.fields || typeof factory.fields !== "object" || Array.isArray(factory.fields) || Object.values(factory.fields).some(value => typeof value !== "string")) throw new Error(`factories[${index}].fields must be an object of strings`);
      if (factory.branches !== undefined && (!Array.isArray(factory.branches) || factory.branches.some(branch => !branch || typeof branch !== "object"))) throw new Error(`factories[${index}].branches must be an array of objects`);
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
    this.resourceNodes.set(expression,nodeID);
    this.graph.nodes.push({id:nodeID,kind,name:resourceName,span:this.span(expression),resource:{valueType:this.resourceValueType(expression),map:constructor.endsWith("Map")}});
  }

  resourceValueType(expression) {
    if (expression.typeArguments?.length) return expression.typeArguments[0].getText(this.tree);
    const codec = this.unwrap(expression.arguments?.[1]);
    if (codec && this.ts.isCallExpression(codec) && this.dexName(codec.expression) === "jsonCodec" && codec.typeArguments?.length) return codec.typeArguments[0].getText(this.tree);
    return (codec && codecValueTypes[this.dexName(codec)]) || "unknown";
  }

  analyzeRegistration(steps) {
    let starts = 0;
    let registrations = 0;
    this.walk(steps.body,node => {
      if (!this.ts.isCallExpression(node) || !this.ts.isPropertyAccessExpression(node.expression)) return;
      const methodName = node.expression.name.text;
      if (!["startStep","otherSteps","withoutStartStep"].includes(methodName)) return;
      for (const [index,argument] of node.arguments.entries()) {
        registrations++;
        const isStart = methodName === "startStep" && index === 0;
        const {target,instance,isReported} = this.registrationTarget(argument);
        if (isReported) continue;
        if (!target) {
          this.reportMissingManifest(argument);
          this.dynamicTarget("registered Step type must be static",argument);
          continue;
        }
        if (this.registered.has(target)) { this.error("duplicate_step_type",`Step ${target} is registered more than once`,argument); continue; }
        this.registered.set(target,`step:${target}`);
        if (instance) {
          this.stepInstances.set(target,instance);
          if (!this.registeredIdentities.has(instance.identity)) this.registeredIdentities.set(instance.identity,target);
        }
        const declaration = instance?.declaration;
        this.graph.nodes.push({id:`step:${target}`,kind:"step",name:target,phase:this.method(declaration,"waitFor") ? "wait_for+execute":"execute",start:isStart,span:this.span(argument)});
        if (isStart) { starts++; this.graph.flow.startStepId = `step:${target}`; }
      }
    });
    if (starts > 1) this.error("multiple_start_steps","Flow defines more than one start Step",steps);
    const stepTypes = this.graph.nodes.filter(node => node.kind === "step").map(node => node.name);
    for (const stepType of [...new Set(stepTypes.filter((name,index) => stepTypes.indexOf(name) !== index))].sort()) {
      this.error("duplicate_step_type",`Step type ${stepType} is registered by more than one Step`,steps);
    }
    if (!registrations && !steps.getText(this.tree).includes("StepList.empty")) this.error("dynamic_step_registration","getSteps must directly call StepList.startStep, withoutStartStep, or otherSteps",steps);
  }

  validateClassCustomization() {
    // The analyzer reads fields and methods from class bodies; these forms make another value answer the read.
    const declarations = new Set([this.flowClass,...[...this.stepInstances.values()].map(instance => instance.declaration),...this.registrationClasses]);
    if (this.hasDynamicFields) this.error("dynamic_class",`${this.escapeText(this.dynamicFieldsCause.node)} ${this.dynamicFieldsCause.reason}, so no Flow or Step field is provable`,this.dynamicFieldsCause.node);
    for (const declaration of declarations) {
      const className = declaration.name?.text || "anonymous";
      const escape = this.escapeCauses.get(declaration);
      if (escape) this.error("dynamic_class",`${this.escapeText(escape.node)} ${escape.reason}${escape.isHeld ? ` and can reach a ${className} object through its fields` : ""}, so class ${className} fields and methods are not provable; use a Flow or Step object only to read a field, register it, target it, or assign it to a name or field`,escape.node);
      if (this.hasDynamicFields || this.dynamicClasses.has(declaration)) this.error("dynamic_class",`class ${className} fields and methods can change through an escaped object, a reflective write, or a prototype or global object`,declaration);
      const memberDecorators = declaration.members.flatMap(member => this.ts.getDecorators?.(member) || []).filter(decorator => !this.isDexRPCDecorator(decorator));
      const decorators = [...(this.ts.getDecorators?.(declaration) || []),...memberDecorators];
      if (decorators.length) this.error("dynamic_class",`class ${className} uses decorator ${[...new Set(decorators.map(decorator => decorator.expression.getText(this.tree)))].sort().join(", ")}, which can replace the class or its members`,declaration);
      const methods = declaration.members.filter(member => member.name && !this.isStatic(member) && (this.ts.isMethodDeclaration(member) || (this.ts.isPropertyDeclaration(member) && member.initializer && this.method(declaration,this.propertyName(member.name)) === this.unwrap(member.initializer))));
      const reassigned = [...new Set(methods.map(member => this.propertyName(member.name)))].filter(name => this.thisStores.get(declaration)?.has(name) || this.isForeignField(name)).sort();
      if (reassigned.length) this.error("dynamic_class",`class ${className} method ${reassigned.join(", ")} is reassigned outside the class body`,declaration);
    }
  }

  isDexRPCDecorator(decorator) {
    const expression = this.unwrap(decorator.expression);
    return this.dexName(this.ts.isCallExpression(expression) ? expression.expression : expression) === "rpc";
  }

  analyzeStep(stepName,nodeID,declaration) {
    this.annotations.push({stepId:nodeID,directives:this.directives(declaration),span:this.span(declaration)});
    const execute = this.method(declaration,"execute");
    if (!execute) this.error("step_handler_not_in_file",`Step ${stepName} execute must be defined in the source file`,declaration);
    else this.analyzeDecisions(nodeID,execute);
    const waitFor = this.method(declaration,"waitFor");
    if (waitFor) this.error("typescript_wait_for_unsupported","TypeScript WaitFor analysis is not supported yet",waitFor);
    const options = this.method(declaration,"getStepOptions");
    if (options) this.analyzeExecuteFailure(nodeID,options);
  }

  analyzeTriggerBindings(bindings) {
    if (!bindings) return;
    const statements = bindings.body?.statements;
    const returned = statements?.length === 1 && this.ts.isReturnStatement(statements[0]) ? this.unwrap(statements[0].expression) : undefined;
    if (!returned || !this.ts.isArrayLiteralExpression(returned)) { this.error("connector_trigger_bindings_dynamic","Connector Trigger bindings must be returned as one static array",bindings); return; }
    for (const expression of returned.elements) {
      const use = this.factoryUse(expression);
      if (use && use.factory.kind === "trigger") { this.uses.push(this.factoryResult("",use)); continue; }
      this.reportMissingManifest(expression);
      this.error("connector_trigger_binding_invalid","Connector Trigger binding must call a manifest factory",expression);
    }
  }

  reportMissingManifest(expression) {
    expression = this.resolvedValue(expression,this.currentFrame).node;
    if (!expression || !this.ts.isCallExpression(expression)) return;
    const callee = expression.expression.getText(this.tree);
    const message = this.missingFactories.get(callee) || this.missingFactories.get(`${callee.split(".")[0]}.`);
    if (message) this.error("connector_manifest_missing",`${callee} is not a manifest Connector factory: ${message}`,expression);
  }

  factoryUse(expression) {
    expression = this.resolvedValue(expression,this.currentFrame).node;
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

  registrationTarget(argument) {
    const {node,frame} = this.resolvedValue(argument,{});
    if (!node) return {};
    if (this.ts.isNewExpression(node) && this.dexName(node.expression) === "StepRef") {
      this.dynamicTarget("registered Step must be a Step instance or Connector factory, not a StepRef",argument);
      return {isReported:true};
    }
    const connectorTarget = this.connectorStepTarget(node);
    if (connectorTarget !== undefined) return {target:connectorTarget};
    const instance = this.stepInstanceValue(node,frame);
    if (instance) this.registrationClasses.add(instance.declaration);
    if (!instance) {
      const declaration = this.ts.isNewExpression(node) ? this.constructedClass(node,frame) : undefined;
      if (declaration && this.extendsClause(declaration)) {
        this.error("step_inheritance_unsupported",`Step class ${declaration.name.text} extends ${this.extendsClause(declaration).getText(this.tree)}; the analyzer reads Step methods only from classes that do not extend another class`,argument);
        return {isReported:true};
      }
      return {};
    }
    return {target:this.instanceStepType(instance,true),instance};
  }

  connectorStepTarget(node) {
    const use = this.factoryUse(node);
    if (!use || use.factory.kind === "trigger") return undefined;
    const stepType = this.staticLiteral(use.fields.get(use.factory.fields.stepType));
    if (typeof stepType !== "string" || !stepType) { this.error("connector_factory_step_type","Connector factory requires a non-empty static Step type",node); return ""; }
    const escape = this.escapeCauses.get(use.expression);
    if (escape) this.error("dynamic_step_target",`${this.escapeText(escape.node)} ${escape.reason}${escape.isHeld ? " and can reach it through its fields" : ""}, so Connector Step ${stepType} is not provable`,escape.node);
    const previous = this.connectorSteps.get(stepType);
    if (previous && previous.expression !== use.expression) this.error("duplicate_step_type",`Connector factories define Step type ${stepType} more than once`,node);
    else this.connectorSteps.set(stepType,use);
    return stepType;
  }

  stepTarget(expression) {
    const {node,frame} = this.resolvedValue(expression,this.currentFrame);
    if (!node) return undefined;
    if (this.ts.isNewExpression(node) && this.dexName(node.expression) === "StepRef") {
      const argument = this.unwrap(node.arguments?.[0]);
      return node.arguments?.length === 1 && argument && this.ts.isStringLiteral(argument) ? argument.text : undefined;
    }
    const connectorTarget = this.connectorStepTarget(node);
    if (connectorTarget !== undefined) return connectorTarget || undefined;
    if (this.ts.isClassDeclaration(node)) return this.classStepTarget(node,expression);
    const instance = this.stepInstanceValue(node,frame);
    if (!instance) return undefined;
    const stepType = this.registeredIdentities.get(instance.identity);
    if (!stepType) this.targetFailures.set(expression,"Step instance is not the registered instance; target the registered instance, its class, or a StepRef");
    return stepType;
  }

  classStepTarget(declaration,expression) {
    const candidates = [...this.stepInstances].filter(([,instance]) => instance.declaration === declaration).map(([stepType]) => stepType);
    if (candidates.length === 1) return candidates[0];
    this.targetFailures.set(expression,candidates.length ? `Step class ${declaration.name.text} has ${candidates.length} registered instances; target a registered instance or a StepRef` : `Step class ${declaration.name.text} has no registered instance`);
    return undefined;
  }

  stepInstanceValue(node,frame) {
    if (!node || !this.ts.isNewExpression(node)) return undefined;
    const declaration = this.constructedClass(node,frame);
    if (!declaration || this.extendsClause(declaration)) return undefined;
    return {declaration,newExpression:node,frame,identity:`${node.pos}:${node.end}@${this.frameIdentity(frame)}`};
  }

  constructedClass(newExpression,frame) {
    const {node} = this.resolvedValue(newExpression.expression,frame);
    return node && this.ts.isClassDeclaration(node) && node.name && this.classes.get(node.name.text) === node ? node : undefined;
  }

  instanceStepType(instance,reportErrors) {
    const method = this.method(instance.declaration,"getStepType");
    if (!method) return instance.declaration.name.text;
    const returns = [];
    this.walkStatements(this.handlerStatements(method),"",value => { returns.push(this.staticLiteral(value,{instance})); return true; });
    if (!returns.length || returns.some(value => typeof value !== "string" || value !== returns[0])) {
      if (reportErrors) this.error("dynamic_type_name","getStepType must return one static string literal or a field bound to a literal at registration",method);
      return undefined;
    }
    return returns[0] || instance.declaration.name.text;
  }

  analyzeDecisions(ownerID,handler) {
    const statements = this.handlerStatements(handler);
    const locals = this.localMovements(handler.body);
    const outcomes = [];
    const hiddenDecisions = [];
    this.walkStatements(statements,"",(expression,condition) => {
      const found = this.decisionOutcomes(expression,condition,locals,hiddenDecisions);
      outcomes.push(...found);
      return found.length > 0;
    });
    if (!outcomes.length && !hiddenDecisions.length) hiddenDecisions.push(handler);
    for (const hidden of hiddenDecisions) this.addHiddenDecision(ownerID,hidden);
    if (!outcomes.length) return;
    if (outcomes.length > 1) {
      for (const outcome of outcomes) if (!outcome.condition) outcome.condition = "otherwise";
      this.graph.nodes.push({id:`decision-dispatch:${ownerID}`,kind:"decision_dispatch",name:"Decision",parentId:ownerID,phase:"execute",span:this.span(handler)});
    }
    const decisionIDs = new Set();
    for (const outcome of outcomes) {
      const decisionID = `decision:${ownerID}:${outcome.span.startLine}:${outcome.span.startColumn}`;
      if (decisionIDs.has(decisionID)) continue;
      decisionIDs.add(decisionID);
      const details = {type:outcome.decisionType};
      if (outcome.checkedChannels.length) details.checkedChannels = outcome.checkedChannels;
      const cancellations = outcome.transitions.filter(transition => transition.kind === "cancel").map(transition => ({stepId:this.resolveTarget(transition.target,transition.span,transition.targetNode),scope:transition.label === "withCancelingSiblingSteps" ? "siblings" : "all"}));
      if (cancellations.length) details.cancellations = cancellations;
      this.graph.nodes.push({id:decisionID,kind:"decision",name:outcome.decisionType,parentId:ownerID,condition:outcome.condition,phase:"execute",span:outcome.span,decision:details});
      for (const transition of outcome.transitions) {
        if (transition.kind === "terminal") continue;
        const edge = {kind:transition.kind,from:decisionID,to:this.resolveTarget(transition.target,transition.span,transition.targetNode),label:transition.label,span:transition.span};
        if (transition.multiplicity) edge.multiplicity = transition.multiplicity;
        this.graph.edges.push(edge);
      }
    }
  }

  addHiddenDecision(ownerID,node) {
    const span = this.span(node);
    const nodeID = `unknown:${ownerID}:${span.startLine}:${span.startColumn}`;
    this.graph.nodes.push({id:nodeID,kind:"unknown",name:"Dynamic Dex decision",span});
    this.graph.edges.push({kind:"transition",from:ownerID,to:nodeID,label:"dynamic",span});
    this.error("hidden_dex_decision","execute must directly return a Dex decision such as goTo or gracefulComplete",node);
  }

  walkStatements(statements,condition,visit) {
    let hasOutcome = false;
    for (const statement of statements) {
      if (this.ts.isReturnStatement(statement)) {
        if (statement.expression) hasOutcome = visit(statement.expression,condition) || hasOutcome;
      } else if (this.ts.isBlock(statement)) {
        hasOutcome = this.walkStatements(statement.statements,condition,visit) || hasOutcome;
      } else if (this.ts.isIfStatement(statement)) {
        const branch = statement.expression.getText(this.tree);
        const thenStatements = this.blockStatements(statement.thenStatement);
        const thenHasOutcome = this.walkStatements(thenStatements,this.combine(condition,branch),visit);
        hasOutcome = thenHasOutcome || hasOutcome;
        if (statement.elseStatement) hasOutcome = this.walkStatements(this.blockStatements(statement.elseStatement),this.combine(condition,this.negate(branch)),visit) || hasOutcome;
        else if (thenHasOutcome && this.alwaysExits(thenStatements)) condition = this.combine(condition,this.negate(branch));
      } else if (this.ts.isSwitchStatement(statement)) {
        const subject = statement.expression.getText(this.tree);
        for (const clause of statement.caseBlock.clauses) {
          const label = this.ts.isCaseClause(clause) ? `${subject} === ${clause.expression.getText(this.tree)}` : "default";
          hasOutcome = this.walkStatements(clause.statements,this.combine(condition,label),visit) || hasOutcome;
        }
      } else if (this.ts.isIterationStatement(statement,false) || this.ts.isLabeledStatement(statement)) {
        hasOutcome = this.walkStatements(this.blockStatements(statement.statement),condition,visit) || hasOutcome;
      } else if (this.ts.isTryStatement(statement)) {
        hasOutcome = this.walkStatements(statement.tryBlock.statements,condition,visit) || hasOutcome;
        if (statement.catchClause) hasOutcome = this.walkStatements(statement.catchClause.block.statements,this.combine(condition,"exception"),visit) || hasOutcome;
        if (statement.finallyBlock) hasOutcome = this.walkStatements(statement.finallyBlock.statements,condition,visit) || hasOutcome;
      }
    }
    return hasOutcome;
  }

  decisionOutcomes(expression,condition,locals,hiddenDecisions) {
    expression = this.unwrap(expression);
    if (!expression) return [];
    if (this.ts.isConditionalExpression(expression)) {
      const branch = expression.condition.getText(this.tree);
      return [
        ...this.decisionOutcomes(expression.whenTrue,this.combine(condition,branch),locals,hiddenDecisions),
        ...this.decisionOutcomes(expression.whenFalse,this.combine(condition,this.negate(branch)),locals,hiddenDecisions),
      ];
    }
    const decisionType = this.decisionType(expression);
    if (!decisionType) { hiddenDecisions.push(expression); return []; }
    return [{decisionType,condition,transitions:this.parseDecision(expression,locals),checkedChannels:this.checkedChannels(expression),span:this.span(expression)}];
  }

  decisionType(expression) {
    if (!this.ts.isCallExpression(expression)) return undefined;
    const name = this.dexName(expression.expression);
    if (cancelingDecisions.has(name)) return this.decisionType(this.unwrap(expression.arguments[0]));
    return name === "goTo" || name === "goToMany" || terminalDecisions.has(name) ? name : undefined;
  }

  parseDecision(expression,locals) {
    const name = this.dexName(expression.expression);
    if (cancelingDecisions.has(name)) {
      const decision = this.unwrap(expression.arguments[0]);
      const transitions = decision && this.ts.isCallExpression(decision) ? this.parseDecision(decision,locals) : [];
      for (const argument of expression.arguments.slice(1)) transitions.push({kind:"cancel",target:this.stepTarget(argument),targetNode:argument,label:name,span:this.span(argument)});
      return transitions;
    }
    if (name === "goTo") return [{kind:"transition",target:this.stepTarget(expression.arguments[0]),targetNode:expression.arguments[0],label:"goTo",span:this.span(expression)}];
    if (name === "goToMany") {
      if (!expression.arguments.length) this.dynamicTarget("goToMany must contain a statically analyzable movement",expression);
      return expression.arguments.flatMap(argument => {
        const movements = this.movementArgument(argument,locals);
        if (!movements.length) this.dynamicTarget("fan-out movements must be built directly in the handler",argument);
        return movements;
      });
    }
    if (name === "forceCompleteIfChannelsEmpty") {
      const movement = this.parseMovement(expression.arguments[1]);
      if (movement) return [movement];
    }
    return [{kind:"terminal"}];
  }

  movementArgument(argument,locals) {
    if (this.ts.isSpreadElement(argument)) {
      const spread = this.unwrap(argument.expression);
      if (this.ts.isArrayLiteralExpression(spread)) return spread.elements.flatMap(element => this.movementArgument(element,locals));
      if (this.ts.isIdentifier(spread)) return (locals.get(spread.text) || []).map(movement => ({...movement,multiplicity:movement.multiplicity || "×N"}));
      return this.callbackMovements(spread);
    }
    const movement = this.parseMovement(argument);
    if (movement) return [movement];
    const value = this.unwrap(argument);
    return value && this.ts.isIdentifier(value) ? locals.get(value.text) || [] : [];
  }

  parseMovement(expression) {
    expression = this.unwrap(expression);
    if (!expression || !this.ts.isCallExpression(expression) || !this.ts.isPropertyAccessExpression(expression.expression)) return undefined;
    if (expression.expression.name.text !== "of" || this.dexName(expression.expression.expression) !== "StepMovement" || !expression.arguments.length) return undefined;
    return {kind:"transition",target:this.stepTarget(expression.arguments[0]),targetNode:expression.arguments[0],label:"fan-out",span:this.span(expression)};
  }

  callbackMovements(expression) {
    const movements = [];
    if (!expression || !this.ts.isCallExpression(expression)) return movements;
    for (const argument of expression.arguments) {
      if (!this.ts.isArrowFunction(argument) && !this.ts.isFunctionExpression(argument)) continue;
      this.walk(argument.body,node => {
        const movement = this.parseMovement(node);
        if (movement) movements.push({...movement,multiplicity:"×N"});
      });
    }
    return movements;
  }

  localMovements(body) {
    const result = new Map();
    this.walk(body,node => {
      if (this.ts.isVariableDeclaration(node) && this.ts.isIdentifier(node.name) && node.initializer) {
        const value = this.unwrap(node.initializer);
        const movement = this.parseMovement(value);
        if (movement) result.set(node.name.text,[movement]);
        else if (this.ts.isArrayLiteralExpression(value)) result.set(node.name.text,value.elements.flatMap(element => this.movementArgument(element,result)));
        else if (this.ts.isCallExpression(value)) {
          const movements = this.callbackMovements(value);
          if (movements.length) result.set(node.name.text,movements);
        }
      }
      if (this.ts.isCallExpression(node) && this.ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === "push" && this.ts.isIdentifier(node.expression.expression)) {
        for (const argument of node.arguments) {
          const movement = this.parseMovement(argument);
          if (!movement) continue;
          const name = node.expression.expression.text;
          result.set(name,[...(result.get(name) || []),{...movement,multiplicity:"×N"}]);
        }
      }
    });
    return result;
  }

  checkedChannels(expression) {
    const name = this.dexName(expression.expression);
    if (cancelingDecisions.has(name)) {
      const decision = this.unwrap(expression.arguments[0]);
      return decision && this.ts.isCallExpression(decision) ? this.checkedChannels(decision) : [];
    }
    if (name !== "forceCompleteIfChannelsEmpty") return [];
    const channels = [];
    for (const argument of expression.arguments.slice(2)) {
      const resource = this.resourceFor(argument);
      if (resource?.startsWith("resource:channel:") && !channels.includes(resource)) channels.push(resource);
    }
    return channels;
  }

  analyzeExecuteFailure(ownerID,options) {
    const pending = [options];
    const seen = new Set([options]);
    while (pending.length) {
      this.walk(pending.pop().body,node => {
        if (!this.ts.isCallExpression(node)) return;
        const callee = node.expression;
        if (this.ts.isPropertyAccessExpression(callee) && callee.name.text === "proceedTo" && this.dexName(callee.expression) === "ExecuteFailure") {
          this.addFailureTransition(ownerID,node);
          return;
        }
        const helper = this.optionHelper(callee);
        if (helper && !seen.has(helper)) { seen.add(helper); pending.push(helper); }
      });
    }
  }

  optionHelper(callee) {
    if (this.ts.isIdentifier(callee)) return this.functions.get(callee.text);
    if (!this.ts.isPropertyAccessExpression(callee)) return undefined;
    for (const declaration of this.classes.values()) {
      const helper = this.method(declaration,callee.name.text);
      if (helper) return helper;
    }
    return undefined;
  }

  addFailureTransition(ownerID,call) {
    if (!call.arguments.length) { this.dynamicTarget("execute failure recovery target must be static",call); return; }
    const target = this.stepTarget(call.arguments[0]);
    const targetID = this.resolveTarget(target,this.span(call.arguments[0]),call.arguments[0]);
    if (this.failureTransitions.has(`${ownerID}\n${targetID}`)) return;
    this.failureTransitions.add(`${ownerID}\n${targetID}`);
    const declaration = this.stepInstances.get(target)?.declaration;
    const edge = {kind:"failure_transition",from:ownerID,to:targetID,label:"Execute failure",span:this.span(call)};
    if (declaration) edge.metadata = {skipWaitFor:!this.method(declaration,"waitFor")};
    this.graph.edges.push(edge);
  }

  resolveTarget(target,span,targetNode) {
    if (this.registered.has(target)) return this.registered.get(target);
    const name = target || "Dynamic Step";
    const nodeID = `unknown:step:${name}`;
    if (!this.graph.nodes.some(node => node.id === nodeID)) this.graph.nodes.push({id:nodeID,kind:"unknown",name,span});
    const message = target ? `Step ${target} is not registered in this Flow` : this.targetFailures.get(targetNode) || "Dex transition target must be a registered Step instance, a class with one registered instance, or a new StepRef with a string literal";
    this.addDiagnostic({severity:"error",code:"unknown_step_target",message,...(span ? {span} : {})});
    return nodeID;
  }

  dynamicTarget(message,node) {
    const span = this.span(node);
    this.graph.nodes.push({id:`unknown:step:${span.startLine}:${span.startColumn}`,kind:"unknown",name:"Dynamic Step",span});
    this.error("dynamic_step_target",message,node);
  }

  factoryResult(nodeID,use) {
    return {stepId:nodeID,manifestPath:use.manifestPath,factoryName:use.factory.name,fields:Object.fromEntries([...use.fields].map(([name,value]) => [name,this.scriptValue(value)])),span:this.span(use.expression)};
  }

  scriptValue(expression) {
    const literal = this.staticLiteral(expression);
    if (literal !== undefined) return {literal};
    const resource = this.resourceFor(expression);
    if (resource) return {resource};
    const target = this.stepTarget(expression);
    if (target) return {target:`step:${target}`};
    expression = this.unwrap(expression);
    if (expression && this.ts.isCallExpression(expression)) {
      const callee = expression.expression;
      const name = this.dexName(callee) || callee.getText(this.tree).split(".").at(-1);
      if (name === "stepRef" && expression.arguments.length === 1) {
        const typeName = this.staticLiteral(expression.arguments[0]);
        const node = this.graph.nodes.find(node => node.kind === "step" && node.name === typeName);
        if (node) return {target:node.id};
      }
      if ((name === "goTo" || name === "proceedTo") && expression.arguments.length) return {call:name,arguments:[this.scriptValue(expression.arguments[0])]};
    }
    return {};
  }

  resourceFor(expression) {
    const resolved = this.resolvedValue(expression,this.currentFrame).node;
    if (this.resourceNodes.has(resolved)) return this.resourceNodes.get(resolved);
    const name = this.unwrap(expression)?.getText(this.tree);
    if (!name) return undefined;
    if (this.resources.has(name)) return this.resources.get(name);
    const field = name.split(".").at(-1);
    return name.includes(".") ? this.resources.get(`this.${field}`) : undefined;
  }

  staticLiteral(expression,frame=this.currentFrame) {
    expression = this.resolvedValue(expression,frame).node;
    if (!expression) return undefined;
    if (this.ts.isStringLiteral(expression) || this.ts.isNoSubstitutionTemplateLiteral(expression)) return expression.text;
    if (this.ts.isNumericLiteral(expression)) return Number(expression.text);
    if (expression.kind === this.ts.SyntaxKind.TrueKeyword) return true;
    if (expression.kind === this.ts.SyntaxKind.FalseKeyword) return false;
    if (expression.kind === this.ts.SyntaxKind.NullKeyword) return null;
    if (this.ts.isArrayLiteralExpression(expression)) {
      const values = expression.elements.map(element => this.staticLiteral(element,frame));
      return values.some(value => value === undefined) ? undefined : values;
    }
    if (this.ts.isObjectLiteralExpression(expression)) {
      const object = {};
      for (const property of expression.properties) {
        if (!this.ts.isPropertyAssignment(property)) return undefined;
        const value = this.staticLiteral(property.initializer,frame);
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

  indexBindings() {
    this.scopeBindings = new Map();
    this.identifierWrites = [];
    this.thisStores = new Map();
    this.foreignStores = new Map();
    this.foreignFieldResults = new Map();
    this.computedStores = [];
    this.dynamicClasses = new Set();
    this.hasDynamicFields = false;
    this.dynamicFieldsCause = undefined;
    this.walk(this.tree,node => {
      if (this.ts.isVariableDeclaration(node)) this.addBindingNames(node.name,node,this.enclosingScope(node),false);
      else if (this.ts.isParameter(node)) this.addBindingNames(node.name,node,node.parent,false);
      else if ((this.ts.isFunctionDeclaration(node) || this.ts.isClassDeclaration(node) || this.ts.isEnumDeclaration(node) || this.ts.isModuleDeclaration(node)) && node.name && this.ts.isIdentifier(node.name)) this.addBinding(node.name.text,node,this.enclosingScope(node),false);
      else if ((this.ts.isFunctionExpression(node) || this.ts.isClassExpression(node)) && node.name) this.addBinding(node.name.text,node,this.ts.isFunctionExpression(node) ? node : this.enclosingScope(node),false);
      else if ((this.ts.isImportClause(node) || this.ts.isNamespaceImport(node) || this.ts.isImportSpecifier(node) || this.ts.isImportEqualsDeclaration(node)) && node.name) this.addBinding(node.name.text,node,this.tree,false);
      else if (this.ts.isBinaryExpression(node) && node.operatorToken.kind >= this.ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= this.ts.SyntaxKind.LastAssignment) this.recordWriteTarget(node.left,node);
      else if ((this.ts.isPrefixUnaryExpression(node) || this.ts.isPostfixUnaryExpression(node)) && [this.ts.SyntaxKind.PlusPlusToken,this.ts.SyntaxKind.MinusMinusToken].includes(node.operator)) this.recordWriteTarget(node.operand,node);
      else if (this.ts.isDeleteExpression(node)) this.recordWriteTarget(node.expression,node);
      else if ((this.ts.isForOfStatement(node) || this.ts.isForInStatement(node)) && !this.ts.isVariableDeclarationList(node.initializer)) this.recordWriteTarget(node.initializer,node);
      else if (this.ts.isCallExpression(node)) this.recordReflectiveWrite(node);
      if ((this.ts.isCallExpression(node) || this.ts.isNewExpression(node)) && ["eval","Function"].includes(this.unwrap(node.expression)?.getText(this.tree))) this.error("dynamic_typescript",`${this.unwrap(node.expression).getText(this.tree)} is not supported in Flow source`,node);
    });
    for (const store of this.computedStores) this.recordComputedStore(store);
    for (const write of this.identifierWrites) if (!this.bindingScope(write.target)) this.markAllFieldsDynamic(write.node,"assigns an undeclared global name");
    for (const alias of this.imports.keys()) {
      for (const bindings of this.scopeBindings.values()) for (const binding of bindings.get(alias) || []) {
        if (!this.isDexImportBinding(binding.declaration)) this.error("dex_alias_rebinding",`Dex import alias ${alias} must not be rebound`,binding.declaration);
      }
      for (const write of this.identifierWrites) if (write.name === alias) this.error("dex_alias_rebinding",`Dex import alias ${alias} must not be rebound`,write.node);
    }
  }

  isDexImportBinding(declaration) {
    const clause = this.ts.isImportSpecifier(declaration) ? declaration.parent.parent : this.ts.isNamespaceImport(declaration) ? declaration.parent : undefined;
    return !!clause && this.ts.isImportClause(clause) && clause.parent.moduleSpecifier.text === "@superdurable/dex";
  }

  addBindingNames(name,declaration,scope,isPattern) {
    if (this.ts.isIdentifier(name)) { this.addBinding(name.text,declaration,scope,isPattern); return; }
    for (const element of name.elements || []) if (this.ts.isBindingElement(element)) this.addBindingNames(element.name,declaration,scope,true);
  }

  addBinding(name,declaration,scope,isPattern) {
    if (!this.scopeBindings.has(scope)) this.scopeBindings.set(scope,new Map());
    const bindings = this.scopeBindings.get(scope);
    if (!bindings.has(name)) bindings.set(name,[]);
    bindings.get(name).push({declaration,isPattern});
  }

  enclosingScope(node) {
    for (let parent = node.parent; parent; parent = parent.parent) if (parent === this.tree || this.ts.isFunctionLike(parent)) return parent;
    return this.tree;
  }

  recordWriteTarget(target,writeNode) {
    target = this.unwrap(target);
    if (!target) return;
    const dexRoot = this.ts.isIdentifier(target) ? undefined : this.dexRootName(target);
    if (dexRoot) this.error("dex_alias_rebinding",`Dex import ${dexRoot} must not be modified`,writeNode);
    if (this.ts.isIdentifier(target)) this.identifierWrites.push({name:target.text,node:writeNode,target});
    else if (this.ts.isPropertyAccessExpression(target)) {
      const owner = this.selfReferenceClass(this.unwrap(target.expression));
      const name = target.name.text;
      if (!owner) { this.addForeignStore(name,target.expression); return; }
      if (!this.thisStores.has(owner)) this.thisStores.set(owner,new Map());
      const stores = this.thisStores.get(owner);
      if (!stores.has(name)) stores.set(name,[]);
      stores.get(name).push(target);
    } else if (this.ts.isElementAccessExpression(target)) this.computedStores.push({target,writeNode});
    else if (this.ts.isArrayLiteralExpression(target)) {
      for (const element of target.elements) this.recordWriteTarget(this.ts.isSpreadElement(element) ? element.expression : element,writeNode);
    } else if (this.ts.isObjectLiteralExpression(target)) {
      for (const property of target.properties) {
        if (this.ts.isPropertyAssignment(property)) this.recordWriteTarget(property.initializer,writeNode);
        else if (this.ts.isShorthandPropertyAssignment(property)) this.recordWriteTarget(property.name,writeNode);
        else if (this.ts.isSpreadAssignment(property)) this.recordWriteTarget(property.expression,writeNode);
      }
    } else if (this.ts.isBinaryExpression(target) && target.operatorToken.kind === this.ts.SyntaxKind.EqualsToken) this.recordWriteTarget(target.left,writeNode);
  }

  recordComputedStore({target}) {
    // A literal key names one field; other keys matter only on tracked objects, where they are escapes.
    const receiver = this.localValue(target.expression);
    if (receiver && (this.ts.isObjectLiteralExpression(receiver) || this.ts.isArrayLiteralExpression(receiver))) return;
    const key = this.localValue(target.argumentExpression);
    if (key && (this.ts.isStringLiteral(key) || this.ts.isNoSubstitutionTemplateLiteral(key) || this.ts.isNumericLiteral(key))) this.addForeignStore(key.text,target.expression);
  }

  indexEscapes() {
    // Fields are provable only while each tracked object is only read, registered, targeted, or assigned to a checked alias.
    this.trackedBindings = new Map();
    this.fieldOrigins = new Map();
    this.intrinsicOrigins = new Map();
    this.factoryTargets = new Map();
    this.escapes = [];
    this.escapeCauses = new Map();
    this.dynamicStatics = new Set();
    this.registrationClasses = new Set();
    this.classDeclarations = [];
    this.walk(this.tree,node => { if (this.ts.isClassDeclaration(node)) this.classDeclarations.push(node); });
    for (let isChanged = true; isChanged;) {
      isChanged = false;
      this.originCache = new Map();
      this.walk(this.tree,node => { if (this.recordTrackedUse(node,false)) isChanged = true; });
    }
    this.originCache = new Map();
    this.walk(this.tree,node => {
      this.recordTrackedUse(node,true);
      this.recordClassReference(node);
      this.recordPrototypeOrGlobalAccess(node);
    });
    this.applyEscapes();
  }

  recordTrackedUse(node,isFinal) {
    // Returns true when an alias gains origins from this use; the final pass also records escapes.
    if (this.ts.isParenthesizedExpression(node) || this.ts.isNonNullExpression(node)) return false;
    const origins = this.trackedOrigins(node);
    if (!origins.size) return false;
    let use = node;
    while (this.ts.isParenthesizedExpression(use.parent) || this.ts.isNonNullExpression(use.parent)) use = use.parent;
    const position = this.trackedUsePosition(use,origins);
    if ((position.binding === undefined && "binding" in position) || (position.field && !position.field[1])) position.escape = "is assigned to a name the analyzer cannot follow";
    let isChanged = false;
    if (position.binding) isChanged = this.addOrigins(this.trackedBindings,position.binding,origins) || isChanged;
    if (position.field?.[1]) isChanged = this.addFieldOrigins(position.field[0],position.field[1],origins,position.isIntrinsic) || isChanged;
    if (position.factory) isChanged = this.addOrigins(this.factoryTargets,position.factory,origins) || isChanged;
    if (!isFinal || !position.escape) return isChanged;
    const candidates = position.escapeOrigins || origins;
    const escaping = new Set([...candidates].filter(origin => origin !== "flow"));
    if (candidates.has("flow") && !this.isFlowHandlePosition(use)) escaping.add(this.flowClass);
    if (escaping.size) this.escapes.push({origins:escaping,node:use,reason:position.escape});
    return isChanged;
  }

  trackedOrigins(node) {
    // Returns the origins whose objects node can evaluate to: class declarations, Connector factory calls, or "flow" for a Flow object made outside its class.
    if (!this.originCache.has(node)) {
      this.originCache.set(node,new Set());
      this.originCache.set(node,this.doTrackedOrigins(node));
    }
    return this.originCache.get(node);
  }

  doTrackedOrigins(node) {
    const kinds = this.ts.SyntaxKind;
    if (node.kind === kinds.ThisKeyword) {
      const owner = this.selfReferenceClass(node);
      return new Set(owner ? [owner] : []);
    }
    if (this.ts.isNewExpression(node)) {
      const declaration = this.staticClass(node.expression);
      return new Set(declaration ? [declaration === this.flowClass ? "flow" : declaration] : []);
    }
    if (this.ts.isCallExpression(node)) {
      const factory = this.factories.get(node.expression.getText(this.tree))?.factory;
      return new Set(factory && factory.kind !== "trigger" ? [node] : []);
    }
    if (this.ts.isIdentifier(node)) {
      const key = this.isValueReference(node) && !this.isAssignmentTarget(node) ? this.bindingKey(node) : undefined;
      return new Set(key ? this.trackedBindings.get(key) || [] : []);
    }
    if (this.ts.isPropertyAccessExpression(node)) {
      const origins = new Set();
      if (this.isAssignmentTarget(node)) return origins;
      for (const origin of this.trackedOrigins(node.expression)) for (const held of this.heldOrigins(origin,node.name.text)) origins.add(held);
      return origins;
    }
    if (this.ts.isParenthesizedExpression(node) || this.ts.isNonNullExpression(node)) return this.trackedOrigins(node.expression);
    if (this.ts.isConditionalExpression(node)) return new Set([...this.trackedOrigins(node.whenTrue),...this.trackedOrigins(node.whenFalse)]);
    if (!this.ts.isBinaryExpression(node)) return new Set();
    const operator = node.operatorToken.kind;
    if ([kinds.AmpersandAmpersandToken,kinds.BarBarToken,kinds.QuestionQuestionToken,kinds.AmpersandAmpersandEqualsToken,kinds.BarBarEqualsToken,kinds.QuestionQuestionEqualsToken].includes(operator)) return new Set([...this.trackedOrigins(node.left),...this.trackedOrigins(node.right)]);
    return operator === kinds.EqualsToken || operator === kinds.CommaToken ? this.trackedOrigins(node.right) : new Set();
  }

  heldOrigins(origin,name) {
    // Returns the origins that field name can hold; a Connector Step can hold every target its config received.
    if (origin === "flow") return this.heldOrigins(this.flowClass,name);
    if (this.ts.isCallExpression(origin)) return this.factoryTargets.get(origin) || new Set();
    const origins = new Set();
    for (let declaration = origin, seen = new Set(); declaration && !seen.has(declaration); declaration = this.staticClass(this.extendsClause(declaration)?.expression)) {
      seen.add(declaration);
      for (const held of this.fieldOrigins.get(declaration)?.get(name) || []) origins.add(held);
    }
    return origins;
  }

  trackedUsePosition(use,origins) {
    // Returns the alias that use assigns ({binding}, {field}, {factory}), the reason it escapes ({escape}), or {} for an allowed use.
    // A class getter that returns the object is a field alias, because reading the getter reads that object.
    const parent = use.parent;
    if (this.ts.isConditionalExpression(parent)) return parent.condition === use ? {escape:"is used as a condition"} : {};
    if (this.ts.isBinaryExpression(parent)) return this.binaryUsePosition(use,parent);
    if (this.ts.isVariableDeclaration(parent) && parent.initializer === use) {
      if (!this.ts.isIdentifier(parent.name)) return {escape:"is destructured"};
      const statement = parent.parent?.parent;
      const isExported = !!statement && this.ts.isVariableStatement(statement) && !!statement.modifiers?.some(modifier => modifier.kind === this.ts.SyntaxKind.ExportKeyword);
      return {binding:this.bindingKey(parent.name),...(isExported ? {escape:"is exported"} : {})};
    }
    if (this.ts.isParameter(parent) && parent.initializer === use) return this.ts.isIdentifier(parent.name) ? {binding:this.bindingKey(parent.name)} : {escape:"is destructured"};
    if (this.ts.isPropertyDeclaration(parent) && parent.initializer === use) {
      if (!this.ts.isClassDeclaration(parent.parent) || this.isStatic(parent) || this.ts.isComputedPropertyName(parent.name)) return {escape:"is stored in a static or computed field"};
      if (this.declaresSetter(parent.parent,this.propertyName(parent.name))) return {escape:"is assigned through a setter"};
      return {field:[parent.parent,this.propertyName(parent.name)],isIntrinsic:true};
    }
    if (this.ts.isPropertyAccessExpression(parent) && parent.expression === use) return this.receiverUsePosition(use,parent,origins);
    if (this.ts.isElementAccessExpression(parent) && parent.expression === use) return {escape:"is accessed with a computed key"};
    if (this.ts.isCallExpression(parent)) {
      const index = parent.arguments.indexOf(use);
      if (index < 0) return {escape:"is called"};
      return this.isDexTargetArgument(parent,index) || this.isRegistrationArgument(parent) ? {} : {escape:"is passed as an argument"};
    }
    if (this.ts.isNewExpression(parent) && parent.arguments?.includes(use)) return this.constructorArgumentPosition(use,parent);
    if ((this.ts.isPropertyAssignment(parent) && parent.initializer === use) || (this.ts.isShorthandPropertyAssignment(parent) && parent.name === use)) {
      const factory = this.factoryTargetCall(parent);
      return factory ? {factory} : {escape:"is stored in an object literal"};
    }
    if (this.ts.isVoidExpression(parent) || this.ts.isExpressionStatement(parent)) return {};
    if (this.ts.isReturnStatement(parent)) {
      const getter = this.ts.findAncestor(parent,node => this.ts.isFunctionLike(node));
      if (getter && this.ts.isGetAccessor(getter) && this.ts.isClassDeclaration(getter.parent) && !this.isStatic(getter) && !this.ts.isComputedPropertyName(getter.name)) return {field:[getter.parent,this.propertyName(getter.name)]};
    }
    if (this.ts.isReturnStatement(parent) || (this.ts.isArrowFunction(parent) && parent.body === use)) return {escape:"is returned"};
    if (this.ts.isAsExpression(parent) || this.ts.isTypeAssertionExpression(parent) || this.ts.isSatisfiesExpression(parent)) return {escape:"is cast"};
    if (this.ts.isArrayLiteralExpression(parent) || this.ts.isSpreadElement(parent) || this.ts.isSpreadAssignment(parent)) return {escape:"is stored in an array or spread"};
    if (this.ts.isExportSpecifier(parent) || this.ts.isExportAssignment(parent)) return {escape:"is exported"};
    return {escape:"is used in an expression"};
  }

  binaryUsePosition(use,binary) {
    // Logical operators forward the value to their parent; an assignment makes its target an alias.
    const kinds = this.ts.SyntaxKind;
    const operator = binary.operatorToken.kind;
    if ([kinds.AmpersandAmpersandToken,kinds.BarBarToken,kinds.QuestionQuestionToken,kinds.CommaToken].includes(operator)) return {};
    if (![kinds.EqualsToken,kinds.AmpersandAmpersandEqualsToken,kinds.BarBarEqualsToken,kinds.QuestionQuestionEqualsToken].includes(operator)) return {escape:"is used in an expression"};
    if (binary.left === use) return {};
    const target = this.unwrap(binary.left);
    if (this.ts.isIdentifier(target)) return {binding:this.bindingKey(target)};
    const owner = this.ts.isPropertyAccessExpression(target) ? this.selfReferenceClass(this.unwrap(target.expression)) : undefined;
    if (owner && this.declaresSetter(owner,target.name.text)) return {escape:"is assigned through a setter"};
    if (owner) return {field:[owner,target.name.text],isIntrinsic:!this.isConstructorParameterReference(use,owner)};
    return {escape:"is stored in another object"};
  }

  receiverUsePosition(use,access,origins) {
    // Field reads are allowed; writes only through this; calls only to methods declared in the class body.
    if (this.isWriteTarget(access)) return use.kind === this.ts.SyntaxKind.ThisKeyword ? {} : {escape:"is written through"};
    if (!this.ts.isCallExpression(access.parent) || access.parent.expression !== access) return {};
    const undeclared = new Set([...origins].filter(origin => !this.declaresMethod(origin,access.name.text)));
    return undeclared.size ? {escape:`calls ${access.name.text}, which its class body does not declare as a method`,escapeOrigins:undeclared} : {};
  }

  constructorArgumentPosition(use,call) {
    // A same-file constructor parameter is a checked alias; its parameter property is a field alias.
    const declaration = this.staticClass(call.expression);
    if (!declaration) return {escape:"is passed as an argument"};
    const constructor = this.constructorOf(declaration);
    const index = call.arguments.indexOf(use);
    const parameter = constructor?.parameters[index];
    if (!parameter || !this.ts.isIdentifier(parameter.name) || parameter.dotDotDotToken || call.arguments.slice(0,index).some(argument => this.ts.isSpreadElement(argument)) || this.usesArguments(constructor)) return {escape:"is passed to a constructor parameter that the analyzer cannot follow"};
    return {binding:this.bindingKey(parameter.name),...(this.ts.isParameterPropertyDeclaration(parameter,constructor) ? {field:[declaration,parameter.name.text]} : {})};
  }

  isFlowHandlePosition(use) {
    // Returns true when a Flow object made outside its class leaves through an export, a return, or an imported callee, which the analyzer does not follow.
    let value = use;
    if (this.ts.isArrayLiteralExpression(value.parent)) value = value.parent;
    else if ((this.ts.isPropertyAssignment(value.parent) && value.parent.initializer === value) || this.ts.isShorthandPropertyAssignment(value.parent)) value = value.parent.parent;
    while (this.ts.isParenthesizedExpression(value.parent)) value = value.parent;
    const parent = value.parent;
    if (this.ts.isReturnStatement(parent) || (this.ts.isArrowFunction(parent) && parent.body === value) || this.ts.isExportSpecifier(parent) || this.ts.isExportAssignment(parent)) return true;
    if (this.ts.isVariableDeclaration(parent)) return value === use && parent.initializer === use;
    return (this.ts.isCallExpression(parent) || this.ts.isNewExpression(parent)) && !!parent.arguments?.includes(value) && this.isImportedCallee(parent.expression,new Set());
  }

  isImportedCallee(callee,seen) {
    // Returns true for a function or constructor from another module, or a method of an object such a call created.
    let root = this.unwrap(callee);
    while (root && (this.ts.isPropertyAccessExpression(root) || this.ts.isElementAccessExpression(root) || this.ts.isCallExpression(root))) root = this.unwrap(root.expression);
    if (!root || !this.ts.isIdentifier(root) || seen.has(root)) return false;
    seen.add(root);
    const scope = this.bindingScope(root);
    const declaration = scope && this.staticBinding(scope,root.text);
    if (declaration && (this.ts.isImportSpecifier(declaration) || this.ts.isImportClause(declaration) || this.ts.isNamespaceImport(declaration) || this.ts.isImportEqualsDeclaration(declaration))) return true;
    const value = this.localValue(root);
    return !!value && value !== root && (this.ts.isNewExpression(value) || this.ts.isCallExpression(value)) && this.isImportedCallee(value.expression,seen);
  }

  isDexTargetArgument(call,index) {
    const name = this.dexName(call.expression);
    if (name === "goTo") return index === 0;
    if (cancelingDecisions.has(name)) return index > 0;
    const callee = call.expression;
    if (!this.ts.isPropertyAccessExpression(callee) || index !== 0) return false;
    const owner = this.dexName(callee.expression);
    return (callee.name.text === "of" && owner === "StepMovement") || (callee.name.text === "proceedTo" && (owner === "ExecuteFailure" || owner === "FlowTimeoutHandlerFailure"));
  }

  isRegistrationArgument(call) {
    if (!this.ts.isPropertyAccessExpression(call.expression) || !["startStep","otherSteps","withoutStartStep"].includes(call.expression.name.text)) return false;
    let node = call.expression.expression;
    while (node && this.dexName(node) !== "StepList" && (this.ts.isCallExpression(node) || this.ts.isPropertyAccessExpression(node))) node = node.expression;
    return !!node && this.dexName(node) === "StepList";
  }

  factoryTargetCall(property) {
    // Returns the Connector Step factory call whose inline config holds property as a Step target field.
    const config = property.parent;
    const call = config.parent;
    if (!this.ts.isObjectLiteralExpression(config) || !call || !this.ts.isCallExpression(call) || call.arguments[0] !== config) return undefined;
    const factory = this.factories.get(call.expression.getText(this.tree))?.factory;
    if (!factory || factory.kind === "trigger") return undefined;
    const targetFields = [factory.fields.executeFailure,...(factory.branches || []).map(branch => branch.field)];
    return targetFields.includes(this.propertyName(property.name)) ? call : undefined;
  }

  declaresMethod(origin,name) {
    // Methods of a Connector Step belong to its package, which the analyzer trusts like the SDK.
    if (origin === "flow") return this.declaresMethod(this.flowClass,name);
    if (this.ts.isCallExpression(origin)) return true;
    for (let declaration = origin, seen = new Set(); declaration && !seen.has(declaration); declaration = this.staticClass(this.extendsClause(declaration)?.expression)) {
      seen.add(declaration);
      const method = this.method(declaration,name);
      if (method) return !this.ts.isFunctionExpression(method);
    }
    return false;
  }

  declaresSetter(declaration,name) {
    // A setter receives the assigned object as a parameter that the analyzer does not track.
    for (let current = declaration, seen = new Set(); current && !seen.has(current); current = this.staticClass(this.extendsClause(current)?.expression)) {
      seen.add(current);
      if (current.members.some(member => this.ts.isSetAccessor(member) && !this.isStatic(member) && member.name && this.propertyName(member.name) === name)) return true;
    }
    return false;
  }

  recordClassReference(node) {
    // A same-file class used as a value can change its statics or create instances that the analyzer cannot see.
    if (!this.ts.isIdentifier(node) || !this.isValueReference(node)) return;
    const declaration = this.staticClass(node);
    if (!declaration) return;
    let use = node;
    while (this.ts.isParenthesizedExpression(use.parent) || this.ts.isNonNullExpression(use.parent)) use = use.parent;
    const parent = use.parent;
    if ((this.ts.isNewExpression(parent) && parent.expression === use) || this.ts.isExpressionWithTypeArguments(parent) || this.ts.isExportSpecifier(parent) || this.ts.isExportAssignment(parent)) return;
    if (this.ts.isBinaryExpression(parent) && parent.operatorToken.kind === this.ts.SyntaxKind.InstanceOfKeyword && parent.right === use) return;
    if (this.ts.isPropertyAccessExpression(parent) && parent.expression === use && !this.isWriteTarget(parent)) return;
    if (this.ts.isCallExpression(parent) && parent.arguments.includes(use) && this.isDexTargetArgument(parent,parent.arguments.indexOf(use))) return;
    if (this.ts.isVariableDeclaration(parent) && parent.initializer === use && this.ts.isIdentifier(parent.name)) {
      const scope = this.bindingScope(parent.name);
      if (scope && this.staticBinding(scope,parent.name.text) === parent && this.isDirectStatement(parent,scope)) return;
    }
    this.dynamicStatics.add(declaration);
    const held = new Set();
    for (let current = declaration, seen = new Set(); current && !seen.has(current); current = this.staticClass(this.extendsClause(current)?.expression)) {
      seen.add(current);
      for (const origin of this.intrinsicOrigins.get(current) || []) held.add(origin === "flow" ? this.flowClass : origin);
    }
    if (held.size) this.escapes.push({origins:held,node:use,reason:"is a class whose instances hold Flow or Step objects and is used as a value"});
  }

  recordPrototypeOrGlobalAccess(node) {
    // Prototype and global objects can replace methods of every object, so any access makes all fields unprovable.
    const key = this.ts.isElementAccessExpression(node) ? this.localValue(node.argumentExpression) : undefined;
    const name = this.ts.isPropertyAccessExpression(node) ? node.name.text : key && (this.ts.isStringLiteral(key) || this.ts.isNoSubstitutionTemplateLiteral(key)) ? key.text : undefined;
    const isGlobal = this.ts.isIdentifier(node) && ["globalThis","window","self","global"].includes(node.text) && this.isValueReference(node) && !this.bindingScope(node);
    if (name === "prototype" || name === "__proto__" || isGlobal) this.markAllFieldsDynamic(node,"accesses a prototype or global object");
  }

  applyEscapes() {
    // An escaped object exposes everything its fields hold, and an escaped this can be a subclass instance.
    const pending = this.escapes.flatMap(escape => [...escape.origins].map(origin => ({origin,escape})));
    while (pending.length) {
      const {origin,escape} = pending.pop();
      if (this.escapeCauses.has(origin)) continue;
      this.escapeCauses.set(origin,escape);
      const heldEscape = escape.isHeld ? escape : {...escape,isHeld:true};
      const held = [];
      if (this.ts.isCallExpression(origin)) held.push(...(this.factoryTargets.get(origin) || []));
      else {
        this.dynamicClasses.add(origin);
        for (let declaration = origin, seen = new Set(); declaration && !seen.has(declaration); declaration = this.staticClass(this.extendsClause(declaration)?.expression)) {
          seen.add(declaration);
          for (const origins of this.fieldOrigins.get(declaration)?.values() || []) held.push(...origins);
        }
        held.push(...this.classDeclarations.filter(subclass => this.staticClass(this.extendsClause(subclass)?.expression) === origin));
      }
      for (const heldOrigin of held) pending.push({origin:heldOrigin === "flow" ? this.flowClass : heldOrigin,escape:heldEscape});
    }
  }

  addOrigins(map,key,origins) {
    if (!map.has(key)) map.set(key,new Set());
    const known = map.get(key);
    const size = known.size;
    for (const origin of origins) known.add(origin);
    return known.size !== size;
  }

  addFieldOrigins(declaration,name,origins,isIntrinsic) {
    if (!this.fieldOrigins.has(declaration)) this.fieldOrigins.set(declaration,new Map());
    const isFieldChanged = this.addOrigins(this.fieldOrigins.get(declaration),name,origins);
    return (isIntrinsic && this.addOrigins(this.intrinsicOrigins,declaration,origins)) || isFieldChanged;
  }

  bindingKey(identifier) {
    // Returns one key for every binding of a name in one scope, so a rebound name stays a checked alias.
    const scope = this.bindingScope(identifier);
    return scope ? `${scope.pos}:${scope.end}:${identifier.text}` : undefined;
  }

  staticClass(expression) {
    const node = this.localValue(expression);
    return node && this.ts.isClassDeclaration(node) ? node : undefined;
  }

  isConstructorParameterReference(use,declaration) {
    if (!this.ts.isIdentifier(use)) return false;
    const scope = this.bindingScope(use);
    return !!scope && scope === this.constructorOf(declaration) && this.scopeBindings.get(scope).get(use.text).every(binding => this.ts.isParameter(binding.declaration));
  }

  usesArguments(constructor) {
    let isUsed = false;
    this.walk(constructor.body,node => { if (this.ts.isIdentifier(node) && node.text === "arguments" && this.enclosingNonArrowFunction(node) === constructor) isUsed = true; });
    return isUsed;
  }

  enclosingNonArrowFunction(node) {
    for (let parent = node.parent; parent; parent = parent.parent) if (this.ts.isFunctionLike(parent) && !this.ts.isArrowFunction(parent)) return parent;
    return undefined;
  }

  isValueReference(identifier) {
    // Declaration, property, label, and type names read no value; shorthand properties and local export names do.
    const parent = identifier.parent;
    if (parent.name === identifier && !this.ts.isShorthandPropertyAssignment(parent) && !(this.ts.isExportSpecifier(parent) && !parent.propertyName)) return false;
    if ((parent.propertyName === identifier && !this.ts.isExportSpecifier(parent)) || this.ts.isQualifiedName(parent)) return false;
    if (this.ts.isLabeledStatement(parent) || this.ts.isBreakOrContinueStatement(parent)) return false;
    for (let node = parent; node; node = node.parent) if (this.ts.isTypeNode(node) && !this.ts.isExpressionWithTypeArguments(node)) return false;
    return true;
  }

  isAssignmentTarget(node) {
    // Returns true when node only receives a value: a plain or destructuring assignment target, or a for-in or for-of variable.
    let target = node;
    while (this.isTransparentWrapper(target.parent)) target = target.parent;
    const parent = target.parent;
    if (this.ts.isBinaryExpression(parent) && parent.left === target) return parent.operatorToken.kind === this.ts.SyntaxKind.EqualsToken;
    if ((this.ts.isForOfStatement(parent) || this.ts.isForInStatement(parent)) && parent.initializer === target) return true;
    if (this.ts.isArrayLiteralExpression(parent)) return this.isAssignmentTarget(parent);
    if (this.ts.isSpreadElement(parent) && this.ts.isArrayLiteralExpression(parent.parent)) return this.isAssignmentTarget(parent.parent);
    if ((this.ts.isShorthandPropertyAssignment(parent) && parent.name === target) || (this.ts.isPropertyAssignment(parent) && parent.initializer === target) || this.ts.isSpreadAssignment(parent)) return this.isAssignmentTarget(parent.parent);
    return false;
  }

  isWriteTarget(node) {
    // Returns true when an assignment, update, or delete writes node.
    if (this.isAssignmentTarget(node)) return true;
    let target = node;
    while (this.isTransparentWrapper(target.parent)) target = target.parent;
    const parent = target.parent;
    const kinds = this.ts.SyntaxKind;
    if (this.ts.isBinaryExpression(parent) && parent.left === target) return parent.operatorToken.kind >= kinds.FirstAssignment && parent.operatorToken.kind <= kinds.LastAssignment;
    if (this.ts.isPrefixUnaryExpression(parent) || this.ts.isPostfixUnaryExpression(parent)) return [kinds.PlusPlusToken,kinds.MinusMinusToken].includes(parent.operator);
    return this.ts.isDeleteExpression(parent);
  }

  isTransparentWrapper(node) {
    return !!node && (this.ts.isParenthesizedExpression(node) || this.ts.isNonNullExpression(node) || this.ts.isAsExpression(node) || this.ts.isTypeAssertionExpression(node) || this.ts.isSatisfiesExpression(node));
  }

  markAllFieldsDynamic(node,reason) {
    if (!this.hasDynamicFields) this.dynamicFieldsCause = {node,reason};
    this.hasDynamicFields = true;
  }

  escapeText(node) {
    const text = node.getText(this.tree).replace(/\s+/g," ");
    return text.length > 40 ? `${text.slice(0,37)}...` : text;
  }

  localValue(expression) {
    // Follows only name bindings, so it reads no field while fields are still being indexed.
    let node = this.unwrap(expression);
    for (const seen = new Set(); node && this.ts.isIdentifier(node) && !seen.has(node);) {
      seen.add(node);
      node = this.unwrap(this.nameValue(node,{})?.node);
    }
    return node;
  }

  dexRootName(expression) {
    let node = this.unwrap(expression);
    while (node && (this.ts.isPropertyAccessExpression(node) || this.ts.isElementAccessExpression(node))) node = this.unwrap(node.expression);
    return node && this.ts.isIdentifier(node) && this.imports.has(node.text) ? node.text : undefined;
  }

  addForeignStore(name,owner) {
    if (!this.foreignStores.has(name)) this.foreignStores.set(name,[]);
    this.foreignStores.get(name).push(owner);
  }

  isForeignField(name) {
    // A write through another object poisons the field unless that object is provably a fresh literal.
    if (!this.foreignFieldResults.has(name)) {
      this.foreignFieldResults.set(name,true);
      const isForeign = (this.foreignStores.get(name) || []).some(owner => {
        const {node} = this.resolvedValue(owner,{});
        return !node || !(this.ts.isObjectLiteralExpression(node) || this.ts.isArrayLiteralExpression(node));
      });
      this.foreignFieldResults.set(name,isForeign);
    }
    return this.foreignFieldResults.get(name);
  }

  recordReflectiveWrite(call) {
    const callee = this.unwrap(call.expression)?.getText(this.tree);
    if (!["Object.assign","Object.defineProperty","Object.defineProperties","Object.setPrototypeOf","Reflect.set","Reflect.defineProperty","Reflect.setPrototypeOf"].includes(callee)) return;
    const target = this.unwrap(call.arguments[0]);
    const dexRoot = this.dexRootName(target);
    if (dexRoot) this.error("dex_alias_rebinding",`Dex import ${dexRoot} must not be modified`,call);
    if (target && (this.ts.isObjectLiteralExpression(target) || this.ts.isArrayLiteralExpression(target))) return;
    if (target?.kind === this.ts.SyntaxKind.ThisKeyword) { this.markDynamicFields(target); return; }
    const key = this.unwrap(call.arguments[1]);
    if (["Object.defineProperty","Reflect.set","Reflect.defineProperty"].includes(callee) && key && (this.ts.isStringLiteral(key) || this.ts.isNoSubstitutionTemplateLiteral(key))) this.addForeignStore(key.text,target);
    else this.markAllFieldsDynamic(call,`calls ${callee} on an object the analyzer cannot identify`);
  }

  markDynamicFields(thisNode) {
    const owner = this.selfReferenceClass(thisNode);
    if (owner) this.dynamicClasses.add(owner);
    else this.markAllFieldsDynamic(thisNode,"writes reflectively through a this the analyzer cannot identify");
  }

  resolvedValue(expression,frame) {
    // Follows provable bindings; an Identifier or property access result means one was not provable.
    let current = {node:this.unwrap(expression),frame};
    const seen = new Set();
    while (current.node) {
      const key = `${current.node.pos}:${current.node.end}:${current.node.kind}@${this.frameIdentity(current.frame)}:${current.frame.initializer?.pos ?? ""}`;
      if (seen.has(key)) break;
      seen.add(key);
      const following = this.ts.isIdentifier(current.node) ? this.nameValue(current.node,current.frame) : this.ts.isPropertyAccessExpression(current.node) ? this.propertyValue(current.node,current.frame) : undefined;
      if (!following) break;
      current = {node:this.unwrap(following.node),frame:following.frame};
    }
    return current;
  }

  frameIdentity(frame) { return frame.instance ? frame.instance.identity : ""; }

  nameValue(identifier,frame) {
    const scope = this.bindingScope(identifier);
    const declaration = scope && this.staticBinding(scope,identifier.text);
    if (!declaration) return undefined;
    if (this.ts.isParameter(declaration)) return frame.initializer === scope ? frame.args.get(identifier.text) : undefined;
    if (this.ts.isClassDeclaration(declaration)) return this.isUsedBeforeBinding(identifier,declaration,scope) ? undefined : {node:declaration,frame};
    if (!this.ts.isVariableDeclaration(declaration) || !declaration.initializer || !this.isDirectStatement(declaration,scope) || this.isUsedBeforeBinding(identifier,declaration,scope)) return undefined;
    return {node:declaration.initializer,frame:scope === this.tree ? {} : frame};
  }

  bindingScope(identifier) {
    for (let node = identifier.parent; node; node = node.parent) {
      if ((node === this.tree || this.ts.isFunctionLike(node)) && this.scopeBindings.get(node)?.has(identifier.text)) return node;
    }
    return undefined;
  }

  staticBinding(scope,name) {
    const bindings = this.scopeBindings.get(scope).get(name);
    if (bindings.length !== 1 || bindings[0].isPattern) return undefined;
    if (this.identifierWrites.some(write => write.name === name && this.isWithin(write.node,scope))) return undefined;
    return bindings[0].declaration;
  }

  isWithin(node,ancestor) {
    for (let current = node; current; current = current.parent) if (current === ancestor) return true;
    return false;
  }

  isDirectStatement(declaration,scope) {
    const statement = declaration.parent?.parent;
    if (!statement || !this.ts.isVariableStatement(statement)) return false;
    return scope === this.tree ? statement.parent === this.tree : statement.parent === scope.body;
  }

  isUsedBeforeBinding(reference,declaration,scope) {
    for (let node = reference.parent; node && node !== scope; node = node.parent) {
      if (this.ts.isFunctionLike(node) || (this.ts.isPropertyDeclaration(node) && !this.isStatic(node))) return false;
    }
    return reference.getStart(this.tree) < declaration.end;
  }

  propertyValue(access,frame) {
    const owner = this.resolvedValue(access.expression,frame);
    const ownerClass = this.selfReferenceClass(owner.node);
    const name = access.name.text;
    if (!ownerClass) return owner.node && this.ts.isClassDeclaration(owner.node) ? this.classStaticValue(owner.node,name) : undefined;
    if (ownerClass === this.flowClass) return this.flowFieldValue(name,access,frame);
    return owner.frame.instance?.declaration === ownerClass ? this.stepFieldValue(owner.frame.instance,name,access,frame) : undefined;
  }

  selfReferenceClass(node) {
    if (node?.kind !== this.ts.SyntaxKind.ThisKeyword) return undefined;
    for (let current = node.parent; current; current = current.parent) {
      if (this.ts.isArrowFunction(current)) continue;
      if (this.ts.isFunctionDeclaration(current) || this.ts.isFunctionExpression(current) || this.ts.isClassStaticBlockDeclaration(current)) return undefined;
      if (this.ts.isMethodDeclaration(current) || this.ts.isConstructorDeclaration(current) || this.ts.isGetAccessor(current) || this.ts.isSetAccessor(current) || this.ts.isPropertyDeclaration(current)) {
        // An object literal or class expression member binds this to its own object.
        return this.isStatic(current) || !this.ts.isClassDeclaration(current.parent) ? undefined : current.parent;
      }
    }
    return undefined;
  }

  flowFieldValue(name,read,readFrame) {
    const declaration = this.flowClass;
    if (this.hasDynamicFields || this.isForeignField(name) || !this.hasModeledClass(declaration)) return undefined;
    const stores = this.fieldStores(declaration,name);
    if (stores.length !== 1 || this.isReadBeforeStore(read,readFrame,stores[0],declaration)) return undefined;
    if (this.ts.isPropertyDeclaration(stores[0])) return {node:stores[0].initializer,frame:{}};
    if (this.ts.isParameter(stores[0])) return undefined;
    return this.constructorStoreValue(declaration,stores[0],{});
  }

  stepFieldValue(instance,name,read,readFrame) {
    const declaration = instance.declaration;
    if (this.hasDynamicFields || this.isForeignField(name) || !this.hasModeledConstruction(declaration)) return undefined;
    const stores = this.fieldStores(declaration,name);
    if (stores.length !== 1 || this.isReadBeforeStore(read,readFrame,stores[0],declaration)) return undefined;
    if (this.ts.isPropertyDeclaration(stores[0])) return {node:stores[0].initializer,frame:{instance}};
    const constructor = this.constructorOf(declaration);
    const args = this.constructorArguments(constructor,instance);
    if (!args) return undefined;
    const frame = {instance,initializer:constructor,args};
    if (this.ts.isParameter(stores[0])) return this.ts.isIdentifier(stores[0].name) && this.staticBinding(constructor,stores[0].name.text) ? args.get(stores[0].name.text) : undefined;
    return this.constructorStoreValue(declaration,stores[0],frame);
  }

  classStaticValue(declaration,name) {
    if (this.hasDynamicFields || this.dynamicStatics.has(declaration) || this.isForeignField(name)) return undefined;
    const members = declaration.members.filter(member => member.name && this.propertyName(member.name) === name && this.isStatic(member));
    if (members.length !== 1 || !this.ts.isPropertyDeclaration(members[0]) || !members[0].initializer) return undefined;
    return {node:members[0].initializer,frame:{}};
  }

  fieldStores(declaration,name) {
    // An accessor answers the read itself, so it counts as another store and the field stays unproven.
    const stores = declaration.members.filter(member => ((this.ts.isPropertyDeclaration(member) && member.initializer) || this.ts.isGetAccessor(member) || this.ts.isSetAccessor(member)) && !this.isStatic(member) && this.propertyName(member.name) === name);
    const constructor = this.constructorOf(declaration);
    for (const parameter of constructor?.parameters || []) {
      if (this.ts.isParameterPropertyDeclaration(parameter,constructor) && this.ts.isIdentifier(parameter.name) && parameter.name.text === name) stores.push(parameter);
    }
    return [...stores,...(this.thisStores.get(declaration)?.get(name) || [])];
  }

  isReadBeforeStore(read,readFrame,store,declaration) {
    // Construction code that runs before the store sees no field yet.
    while (readFrame.instance && this.isConstructionCode(read,readFrame.instance.declaration)) {
      read = readFrame.instance.newExpression;
      readFrame = readFrame.instance.frame;
    }
    const constructor = this.constructorOf(declaration);
    for (let node = read.parent; node; node = node.parent) {
      if (constructor && node === constructor) return !this.ts.isPropertyDeclaration(store) && !this.ts.isParameter(store) && read.getStart(this.tree) < store.parent.end;
      if (this.ts.isPropertyDeclaration(node) && node.parent === declaration && !this.isStatic(node)) return !this.ts.isPropertyDeclaration(store) || store.pos >= node.pos;
      if (this.ts.isFunctionLike(node)) return false;
    }
    return false;
  }

  isConstructionCode(node,declaration) {
    const constructor = this.constructorOf(declaration);
    for (let current = node.parent; current; current = current.parent) {
      if (constructor && current === constructor) return true;
      if (this.ts.isPropertyDeclaration(current) && current.parent === declaration && !this.isStatic(current)) return true;
      if (this.ts.isFunctionLike(current)) return false;
    }
    return false;
  }

  constructorStoreValue(declaration,store,frame) {
    const constructor = this.constructorOf(declaration);
    const assignment = store.parent;
    if (!constructor?.body || !this.ts.isBinaryExpression(assignment) || assignment.left !== store || assignment.operatorToken.kind !== this.ts.SyntaxKind.EqualsToken) return undefined;
    if (!this.ts.isExpressionStatement(assignment.parent) || assignment.parent.parent !== constructor.body) return undefined;
    let hasReturn = false;
    this.walk(constructor.body,node => { if (this.ts.isReturnStatement(node) && this.enclosingScope(node) === constructor) hasReturn = true; });
    return hasReturn ? undefined : {node:assignment.right,frame};
  }

  hasModeledConstruction(declaration) {
    // Inheritance, decorators, reflective writes, or a leaked this can change fields the analyzer cannot see.
    if (!this.hasModeledClass(declaration)) return false;
    const constructor = this.constructorOf(declaration);
    if (!constructor?.body) return true;
    for (const statement of constructor.body.statements) {
      if (this.ts.isVariableStatement(statement)) continue;
      const expression = this.ts.isExpressionStatement(statement) ? statement.expression : undefined;
      if (expression && this.ts.isBinaryExpression(expression) && expression.operatorToken.kind === this.ts.SyntaxKind.EqualsToken && this.ts.isPropertyAccessExpression(expression.left) && expression.left.expression.kind === this.ts.SyntaxKind.ThisKeyword) continue;
      return false;
    }
    let isThisLeaked = false;
    this.walk(constructor.body,node => {
      if (node.kind === this.ts.SyntaxKind.ThisKeyword && !(this.ts.isPropertyAccessExpression(node.parent) && node.parent.expression === node)) isThisLeaked = true;
    });
    return !isThisLeaked;
  }

  hasModeledClass(declaration) {
    // Flow and Step classes share these checks; a constructor return replaces the constructed object.
    if (this.extendsClause(declaration) || this.dynamicClasses.has(declaration) || this.ts.getDecorators?.(declaration)?.length) return false;
    if (declaration.members.some(member => member.name && this.ts.isComputedPropertyName(member.name))) return false;
    const constructor = this.constructorOf(declaration);
    let hasReturn = false;
    this.walk(constructor?.body,node => { if (this.ts.isReturnStatement(node) && this.enclosingScope(node) === constructor) hasReturn = true; });
    return !hasReturn;
  }

  constructorArguments(constructor,instance) {
    const args = new Map();
    if (!constructor) return args;
    const call = instance.newExpression;
    if (call.arguments?.some(argument => this.ts.isSpreadElement(argument))) return undefined;
    const frame = {instance,initializer:constructor,args};
    for (const [index,parameter] of constructor.parameters.entries()) {
      if (!this.ts.isIdentifier(parameter.name) || parameter.dotDotDotToken) continue;
      const argument = call.arguments?.[index];
      if (argument) args.set(parameter.name.text,{node:argument,frame:instance.frame});
      else if (parameter.initializer) args.set(parameter.name.text,{node:parameter.initializer,frame});
    }
    return args;
  }

  constructorOf(declaration) { return declaration.members.find(member => this.ts.isConstructorDeclaration(member) && member.body); }
  extendsClause(declaration) { return declaration.heritageClauses?.find(clause => clause.token === this.ts.SyntaxKind.ExtendsKeyword)?.types[0]; }
  isStatic(member) { return !!member.modifiers?.some(modifier => modifier.kind === this.ts.SyntaxKind.StaticKeyword); }

  typeName(declaration,methodName) {
    const method = this.method(declaration,methodName);
    if (!method) return declaration.name.text;
    const returns = [];
    this.walkStatements(this.handlerStatements(method),"",expression => { returns.push(this.staticLiteral(expression)); return true; });
    if (!returns.length || returns.some(value => typeof value !== "string" || value !== returns[0])) this.error("dynamic_type_name",`${methodName} must return one static string`,method);
    return returns[0] || declaration.name.text;
  }

  dexName(expression) {
    expression = this.unwrap(expression);
    if (!expression) return undefined;
    if (this.ts.isIdentifier(expression)) return this.imports.get(expression.text);
    if (this.ts.isPropertyAccessExpression(expression) && this.imports.get(expression.expression.getText(this.tree)) === "*") return expression.name.text;
    return undefined;
  }

  unwrap(expression) {
    while (expression && (this.ts.isParenthesizedExpression(expression) || this.ts.isAsExpression(expression) || this.ts.isSatisfiesExpression(expression) || this.ts.isNonNullExpression(expression) || this.ts.isAwaitExpression(expression) || this.ts.isTypeAssertionExpression(expression))) expression = expression.expression;
    return expression;
  }

  method(declaration,name) {
    for (const member of declaration?.members || []) {
      if (!member.name || this.propertyName(member.name) !== name) continue;
      if (this.ts.isMethodDeclaration(member)) return member;
      const initializer = this.ts.isPropertyDeclaration(member) ? this.unwrap(member.initializer) : undefined;
      if (initializer && (this.ts.isArrowFunction(initializer) || this.ts.isFunctionExpression(initializer))) return initializer;
    }
    return undefined;
  }

  handlerStatements(handler) {
    if (!handler?.body) return [];
    return this.ts.isBlock(handler.body) ? handler.body.statements : [this.ts.factory.createReturnStatement(handler.body)];
  }

  blockStatements(statement) { return this.ts.isBlock(statement) ? statement.statements : [statement]; }
  alwaysExits(statements) {
    const last = statements.at(-1);
    if (!last) return false;
    if (this.ts.isReturnStatement(last) || this.ts.isThrowStatement(last)) return true;
    if (this.ts.isBlock(last)) return this.alwaysExits(last.statements);
    return this.ts.isIfStatement(last) && !!last.elseStatement && this.alwaysExits(this.blockStatements(last.thenStatement)) && this.alwaysExits(this.blockStatements(last.elseStatement));
  }
  combine(parent,child) { return parent ? `${parent} && ${child}` : child; }
  negate(condition) { return `!(${condition})`; }
  propertyName(name) { return this.ts.isStringLiteral(name) ? name.text : name.getText(this.tree); }
  walk(node,visit) { if (!node) return; visit(node); this.ts.forEachChild(node,child => this.walk(child,visit)); }
  span(node) {
    if (typeof node?.getStart !== "function" || node.pos < 0) return undefined;
    const start = this.tree.getLineAndCharacterOfPosition(node.getStart(this.tree));
    const end = this.tree.getLineAndCharacterOfPosition(node.end);
    return {startLine:start.line+1,startColumn:start.character+1,endLine:end.line+1,endColumn:end.character+1};
  }
  error(code,message,node) {
    const span = this.span(node);
    this.addDiagnostic({severity:"error",code,message,...(span ? {span} : {})});
  }
  addDiagnostic(diagnostic) {
    this.graph.valid = false;
    const key = JSON.stringify(diagnostic);
    if (!this.graph.diagnostics.some(existing => JSON.stringify(existing) === key)) this.graph.diagnostics.push(diagnostic);
  }
  result() { return {graph:this.graph,annotations:this.annotations,factories:this.uses}; }
}

try {
  process.stdout.write(JSON.stringify(new TypeScriptAnalyzer(JSON.parse(fs.readFileSync(0,"utf8"))).analyze()));
} catch (error) {
  process.stderr.write(error.message);
  process.exitCode = 1;
}
