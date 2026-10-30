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
    this.instanceFields = new Map();
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
    const flows = [...this.classes.values()].filter(declaration => this.method(declaration,"getSteps"));
    if (flows.length !== 1) {
      this.error(flows.length ? "multiple_flows" : "flow_not_found", "source must define exactly one Flow");
      return this.result();
    }
    const flow = flows[0];
    this.flowClass = flow;
    this.graph.flow = {name:this.typeName(flow,"getFlowType"),span:this.span(flow)};
    for (const member of flow.members) {
      if (this.ts.isPropertyDeclaration(member)) this.indexValue(member.name,member.initializer,"this.");
      if (this.ts.isConstructorDeclaration(member) && member.body) {
        for (const [name,expression] of this.statementValues(member.body.statements)) this.values.set(name,expression);
      }
    }
    for (const declaration of this.classes.values()) for (const member of declaration.members) {
      if (this.ts.isPropertyDeclaration(member) && member.modifiers?.some(modifier => modifier.kind === this.ts.SyntaxKind.StaticKeyword)) this.indexValue(member.name,member.initializer,`${declaration.name.text}.`);
    }
    for (const [name,expression] of this.values) this.indexResource(name,expression);
    this.analyzeRegistration(this.method(flow,"getSteps"));
    for (const [stepName,nodeID] of this.registered) {
      const connector = this.connectorSteps.get(stepName);
      if (connector) { this.uses.push(this.factoryResult(nodeID,connector)); continue; }
      const instance = this.stepInstances.get(stepName);
      this.instanceFields = instance.fields;
      this.analyzeStep(stepName,nodeID,instance.declaration);
      this.instanceFields = new Map();
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
        const target = this.stepTarget(argument,true);
        if (!target) {
          this.reportMissingManifest(argument);
          this.dynamicTarget("registered Step type must be static",argument);
          continue;
        }
        if (this.registered.has(target)) { this.error("duplicate_step_type",`Step ${target} is registered more than once`,argument); continue; }
        const instance = this.stepInstance(argument);
        if (!instance && !this.connectorSteps.has(target)) {
          this.dynamicTarget("registered Step must be a local instance or Connector factory, not a StepRef",argument);
          continue;
        }
        this.registered.set(target,`step:${target}`);
        if (instance) this.stepInstances.set(target,instance);
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
    expression = this.resolveValue(expression);
    if (!expression || !this.ts.isCallExpression(expression)) return;
    const callee = expression.expression.getText(this.tree);
    const message = this.missingFactories.get(callee) || this.missingFactories.get(`${callee.split(".")[0]}.`);
    if (message) this.error("connector_manifest_missing",`${callee} is not a manifest Connector factory: ${message}`,expression);
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

  stepTarget(expression,reportErrors=false) {
    expression = this.resolveValue(expression);
    if (!expression) return undefined;
    if ((this.ts.isNewExpression(expression) || this.ts.isCallExpression(expression)) && this.dexName(expression.expression) === "StepRef") {
      const argument = this.unwrap(expression.arguments?.[0]);
      return expression.arguments?.length === 1 && argument && this.ts.isStringLiteral(argument) ? argument.text : undefined;
    }
    const use = this.factoryUse(expression);
    if (use && use.factory.kind !== "trigger") {
      const stepType = this.staticLiteral(use.fields.get(use.factory.fields.stepType));
      if (typeof stepType !== "string" || !stepType) { this.error("connector_factory_step_type","Connector factory requires a non-empty static Step type",expression); return undefined; }
      const previous = this.connectorSteps.get(stepType);
      if (previous && previous.expression !== use.expression) this.error("duplicate_step_type",`Connector factories define Step type ${stepType} more than once`,expression);
      else this.connectorSteps.set(stepType,use);
      return stepType;
    }
    if (!reportErrors && this.ts.isIdentifier(expression) && this.classes.has(expression.text)) {
      const candidates = [...this.stepInstances].filter(([,instance]) => instance.declaration.name.text === expression.text);
      return candidates.length === 1 ? candidates[0][0] : undefined;
    }
    return this.stepInstance(expression,reportErrors)?.stepType;
  }

  stepInstance(expression,reportErrors=false) {
    expression = this.resolveValue(expression);
    if (!expression) return undefined;
    const constructor = this.ts.isNewExpression(expression) ? expression : undefined;
    const declaration = this.classes.get((constructor?.expression || expression).getText(this.tree));
    if (!declaration) return undefined;
    const fields = new Map();
    for (const member of declaration.members) if (this.ts.isPropertyDeclaration(member) && member.initializer) fields.set(`this.${this.propertyName(member.name)}`,member.initializer);
    const initializer = declaration.members.find(member => this.ts.isConstructorDeclaration(member));
    if (initializer && constructor) {
      const argumentsByName = new Map();
      for (const [index,parameter] of initializer.parameters.entries()) {
        const argument = this.resolveValue(constructor.arguments?.[index] || parameter.initializer);
        argumentsByName.set(parameter.name.getText(this.tree),argument);
        if (parameter.modifiers?.length) fields.set(`this.${parameter.name.getText(this.tree)}`,argument);
      }
      for (const [name,value] of this.statementValues(initializer.body?.statements || [])) {
        if (name.startsWith("this.")) fields.set(name,value && this.ts.isIdentifier(value) && argumentsByName.has(value.text) ? argumentsByName.get(value.text) : value);
      }
    }
    const method = this.method(declaration,"getStepType");
    if (!method) return {declaration,fields,stepType:declaration.name.text};
    const returns = [];
    this.walkStatements(this.handlerStatements(method),"",value => { returns.push(this.staticLiteral(value,new Set(),fields)); return true; });
    if (!returns.length || returns.some(value => typeof value !== "string" || value !== returns[0])) {
      if (reportErrors) this.error("dynamic_type_name","getStepType must return one static string literal or a field bound to a literal at registration",method);
      return undefined;
    }
    return {declaration,fields,stepType:returns[0] || declaration.name.text};
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
      const cancellations = outcome.transitions.filter(transition => transition.kind === "cancel").map(transition => ({stepId:this.resolveTarget(transition.target,transition.span),scope:transition.label === "withCancelingSiblingSteps" ? "siblings" : "all"}));
      if (cancellations.length) details.cancellations = cancellations;
      this.graph.nodes.push({id:decisionID,kind:"decision",name:outcome.decisionType,parentId:ownerID,condition:outcome.condition,phase:"execute",span:outcome.span,decision:details});
      for (const transition of outcome.transitions) {
        if (transition.kind === "terminal") continue;
        const edge = {kind:transition.kind,from:decisionID,to:this.resolveTarget(transition.target,transition.span),label:transition.label,span:transition.span};
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
      for (const argument of expression.arguments.slice(1)) transitions.push({kind:"cancel",target:this.stepTarget(argument),label:name,span:this.span(argument)});
      return transitions;
    }
    if (name === "goTo") return [{kind:"transition",target:this.stepTarget(expression.arguments[0]),label:"goTo",span:this.span(expression)}];
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
    return {kind:"transition",target:this.stepTarget(expression.arguments[0]),label:"fan-out",span:this.span(expression)};
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
    const targetID = this.resolveTarget(target,this.span(call.arguments[0]));
    if (this.failureTransitions.has(`${ownerID}\n${targetID}`)) return;
    this.failureTransitions.add(`${ownerID}\n${targetID}`);
    const declaration = this.stepInstances.get(target)?.declaration;
    const edge = {kind:"failure_transition",from:ownerID,to:targetID,label:"Execute failure",span:this.span(call)};
    if (declaration) edge.metadata = {skipWaitFor:!this.method(declaration,"waitFor")};
    this.graph.edges.push(edge);
  }

  resolveTarget(target,span) {
    if (this.registered.has(target)) return this.registered.get(target);
    const name = target || "Dynamic Step";
    const nodeID = `unknown:step:${name}`;
    if (!this.graph.nodes.some(node => node.id === nodeID)) this.graph.nodes.push({id:nodeID,kind:"unknown",name,span});
    this.graph.valid = false;
    this.graph.diagnostics.push({severity:"error",code:"unknown_step_target",message:target ? `Step ${target} is not registered in this Flow` : "Dex transition target must resolve to a registered Step instance, an unambiguous class, or a literal StepRef",...(span ? {span} : {})});
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
    const name = this.unwrap(expression)?.getText(this.tree);
    if (!name) return undefined;
    if (this.resources.has(name)) return this.resources.get(name);
    const field = name.split(".").at(-1);
    return name.includes(".") ? this.resources.get(`this.${field}`) : undefined;
  }

  staticLiteral(expression,seen=new Set(),fields=this.instanceFields) {
    expression = this.resolveValue(expression,fields);
    if (!expression) return undefined;
    if (this.ts.isStringLiteral(expression) || this.ts.isNoSubstitutionTemplateLiteral(expression)) return expression.text;
    if (this.ts.isNumericLiteral(expression)) return Number(expression.text);
    if (expression.kind === this.ts.SyntaxKind.TrueKeyword) return true;
    if (expression.kind === this.ts.SyntaxKind.FalseKeyword) return false;
    if (expression.kind === this.ts.SyntaxKind.NullKeyword) return null;
    const name = expression.getText(this.tree);
    if (this.values.has(name) && !seen.has(name)) { const next = new Set(seen); next.add(name); return this.staticLiteral(this.values.get(name),next,fields); }
    if (this.ts.isArrayLiteralExpression(expression)) {
      const values = expression.elements.map(element => this.staticLiteral(element,seen,fields));
      return values.some(value => value === undefined) ? undefined : values;
    }
    if (this.ts.isObjectLiteralExpression(expression)) {
      const object = {};
      for (const property of expression.properties) {
        if (!this.ts.isPropertyAssignment(property)) return undefined;
        const value = this.staticLiteral(property.initializer,seen,fields);
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

  statementValues(statements) {
    const values = new Map();
    for (const statement of statements) {
      if (this.ts.isFunctionDeclaration(statement) || this.ts.isClassDeclaration(statement)) continue;
      this.walk(statement,node => {
        if (this.ts.isVariableDeclaration(node)) {
          const name = node.name.getText(this.tree);
          const isStatic = this.ts.isVariableStatement(statement) && node.parent === statement.declarationList && !values.has(name);
          values.set(name,isStatic ? node.initializer : undefined);
        } else if (this.ts.isBinaryExpression(node) && node.operatorToken.kind >= this.ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= this.ts.SyntaxKind.LastAssignment) {
          const name = node.left.getText(this.tree);
          const isStatic = this.ts.isExpressionStatement(statement) && statement.expression === node && node.operatorToken.kind === this.ts.SyntaxKind.EqualsToken && !values.has(name);
          values.set(name,isStatic ? node.right : undefined);
        } else if ((this.ts.isPrefixUnaryExpression(node) || this.ts.isPostfixUnaryExpression(node)) && [this.ts.SyntaxKind.PlusPlusToken,this.ts.SyntaxKind.MinusMinusToken].includes(node.operator)) {
          values.set(node.operand.getText(this.tree),undefined);
        }
      });
    }
    return values;
  }

  resolveValue(expression,fields=this.instanceFields) {
    const values = new Map([...this.values,...fields]);
    for (let parent = expression?.parent; parent; parent = parent.parent) {
      if (this.ts.isFunctionLike(parent) && parent.body && this.ts.isBlock(parent.body)) {
        for (const parameter of parent.parameters) values.set(parameter.name.getText(this.tree),undefined);
        for (const [name,value] of this.statementValues(parent.body.statements)) values.set(name,value);
        break;
      }
    }
    const seen = new Set();
    expression = this.unwrap(expression);
    while (expression && values.has(expression.getText(this.tree))) {
      const name = expression.getText(this.tree);
      if (seen.has(name)) return undefined;
      seen.add(name);
      expression = this.unwrap(values.get(name));
    }
    return expression;
  }

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
    this.graph.valid = false;
    this.graph.diagnostics.push({severity:"error",code,message,...(span ? {span} : {})});
  }
  result() { return {graph:this.graph,annotations:this.annotations,factories:this.uses}; }
}

try {
  process.stdout.write(JSON.stringify(new TypeScriptAnalyzer(JSON.parse(fs.readFileSync(0,"utf8"))).analyze()));
} catch (error) {
  process.stderr.write(error.message);
  process.exitCode = 1;
}
