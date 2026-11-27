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
// Wait factory names map to the Go analyzer's Wait types.
const waitTypes = new Map([["skipImmediately","skipWaitImmediately"],["until","until"],["allOf","allOf"],["anyOf","anyOf"],["anyCombinationOf","anyComboOf"]]);
const channelConditionMethods = new Set(["forOne","forN","atLeast","atMost","range","forAllBranches","forFirstBranch","forBranches","forRemainingBranches"]);
const resourceEdgeKinds = new Map([
  ...["get","size","getMapSize","getAllInstanceKeys","pendingMessages","findPendingMessage","results","branchResults","remainingBranchResults"].map(method => [method,"resource_read"]),
  ...["set","delete","write"].map(method => [method,"resource_write"]),
  ...["publish","endBranch","reportBranchResult"].map(method => [method,"resource_publish"]),
]);
const maximumHelperDepth = 6;
const resourceMethodNames = new Set([...resourceEdgeKinds.keys(),"bufferedText","fork","forkWithRoundKey"]);
const resourceClassNames = new Set(["Attribute","AttributeMap","Channel","ChannelMap","Join","Stream"]);
const attributeOptionMethods = new Set(["syncToAttributeStore","hardDeleteOnNull","plaintext","writesRequireLock","writesRequireAttributeLock"]);
const channelOptionMethods = new Set(["publishesRequireAttributeLock"]);
const definitionOptionMethods = new Set([...attributeOptionMethods,...channelOptionMethods]);
// A chained option counts only on the constructors that define it.
const definitionOptionMethodsByConstructor = {Attribute:attributeOptionMethods,AttributeMap:attributeOptionMethods,Channel:channelOptionMethods,ChannelMap:channelOptionMethods};
const contextTypeNames = new Set(["Context","AsyncContext"]);
const reflectiveWriteNames = new Set(["Object.assign","Object.defineProperty","Object.defineProperties","Object.setPrototypeOf","Reflect.set","Reflect.defineProperty","Reflect.setPrototypeOf","Reflect.deleteProperty"]);
const builtinClassNames = new Set(["Map","WeakMap","Set","WeakSet","Array","Date","RegExp","Error","Promise","URL"]);
// Dex SDK exports that reach the Dex server, the network, or files; every other Dex export stays inside the Step.
const dexExternalExports = new Set(["Client","Worker","PullWorker","openBlobCache","FileCredential","ApiKeyCredential","FilePayloadEncryptionKeyProvider"]);
// Default-library interfaces, namespaces, and globals whose members do no I/O.
const pureLibraryContainers = new Set([
  "String","StringConstructor","Number","NumberConstructor","Boolean","BooleanConstructor","BigInt","BigIntConstructor",
  "Symbol","SymbolConstructor","Array","ReadonlyArray","ArrayConstructor","Map","ReadonlyMap","MapConstructor",
  "Set","ReadonlySet","SetConstructor","WeakMap","WeakMapConstructor","WeakSet","WeakSetConstructor",
  "Object","ObjectConstructor","Math","JSON","Date","DateConstructor","RegExp","RegExpConstructor",
  "RegExpMatchArray","RegExpExecArray","Promise","PromiseLike","PromiseConstructor","Console","Intl",
  "Iterator","IteratorObject","IterableIterator","ArrayIterator","MapIterator","SetIterator","StringIterator",
  "RegExpStringIterator","Generator","ArrayBuffer","ArrayBufferConstructor","DataView","DataViewConstructor",
  "URL","URLSearchParams","TextEncoder","TextDecoder",
]);
const pureLibraryContainerPattern = /^(?:\w*Error|(?:Big)?(?:Int|Uint|Float)\d+(?:Clamped)?Array)(?:Constructor)?$/;
// Node exports that compute in memory; other Node modules, such as fs, http, and timers, do I/O or wait.
const pureNodeExports = new Map([
  ["crypto",new Set(["createHash","createHmac","randomUUID","timingSafeEqual"])],
  ["path",new Set(["basename","dirname","extname","format","isAbsolute","join","normalize","parse","relative","sep"])],
  ["util",new Set(["format","isDeepStrictEqual"])],
]);
const pureLibraryFunctions = new Set(["parseInt","parseFloat","isNaN","isFinite","encodeURI","encodeURIComponent","decodeURI","decodeURIComponent","structuredClone"]);
// Source files the locked-method check may type-check besides the Flow source; declarations elsewhere stay unresolved.
const projectSourceExtensions = [".ts",".tsx",".mts",".cts"];
const maximumProjectFiles = 200;
const excludedProjectDirectories = new Set(["node_modules","venv",".venv","site-packages"]);
// Library containers whose type arguments, not their methods, decide whether a value is data.
const dataContainerTypeNames = new Set(["Array","ReadonlyArray","Map","ReadonlyMap","Set","ReadonlySet","Promise"]);
const dataLibraryTypeNames = new Set(["Date","RegExp"]);
const maximumDataTypeDepth = 12;

// projectRootDirectory returns the nearest directory above sourcePath with a package.json or tsconfig.json, else the source directory.
function projectRootDirectory(sourcePath) {
  for (let directory = path.dirname(sourcePath); ; directory = path.dirname(directory)) {
    if (fs.existsSync(path.join(directory,"package.json")) || fs.existsSync(path.join(directory,"tsconfig.json"))) return directory;
    if (path.dirname(directory) === directory) return path.dirname(sourcePath);
  }
}

function fallbackOperatorKinds(ts) {
  return new Set([ts.SyntaxKind.QuestionQuestionToken,ts.SyntaxKind.BarBarToken,ts.SyntaxKind.AmpersandAmpersandToken]);
}

class TypeScriptAnalyzer {
  constructor(request) {
    this.path = request.path;
    this.source = request.source;
    this.flowType = request.flowType;
    // dexcli passes a trusted compiler entry; resolving from the source project could run its code.
    let compilerPath = request.compilerEntryPath;
    if (!compilerPath) {
      try {
        compilerPath = createRequire(this.path).resolve("typescript");
      } catch {
        throw new Error("TypeScript analysis requires Node.js and the typescript package installed in the source project or in a node_modules directory listed in NODE_PATH, or a trusted typescript package directory passed with --typescript-compiler or DEX_TYPESCRIPT_COMPILER");
      }
    }
    this.ts = require(compilerPath);
    this.compilerEntryPath = compilerPath;
    this.tree =this.ts.createSourceFile(this.path, this.source, this.ts.ScriptTarget.Latest, true);
    this.graph = {schemaVersion:"2.0", valid:true, source:{language:"typescript",path:this.path}, flow:{name:""}, nodes:[], edges:[], diagnostics:[]};
    this.imports = new Map();
    this.factories = new Map();
    this.missingFactories = new Map();
    this.classes = new Map();
    this.functions = new Map();
    this.values = new Map();
    this.registered = new Map();
    this.stepInstances = new Map();
    this.registeredIdentities = new Map();
    this.resourceNodes = new Map();
    this.attributeDeclarations = new Map();
    this.persistenceResourceIds = null;
    this.diagnosticResourceIds = {};
    this.joinResourceIDs = new Set();
    this.targetFailures = new Map();
    this.currentFrame = {};
    this.connectorSteps = new Map();
    this.failureTransitions = new Set();
    this.annotations = [];
    this.uses = [];
    this.actions = [];
    this.rpcLockErrors = [];
    this.viewRPCs = [];
    this.inputTypes = new Map();
    this.undrawnLockEdges = [];
    this.lockedMethodCandidates = [];
    this.methodExternalCalls = [];
    this.exactHelperResults = new Map();
    this.helpersInProgress = new Set();
  }

  analyze() {
    for (const diagnostic of this.tree.parseDiagnostics) {
      this.error("typescript_parse_failed", this.ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"));
    }
    if (!this.graph.valid) return this.result();
    for (const statement of this.tree.statements) {
      if ((this.ts.isInterfaceDeclaration(statement) || this.ts.isTypeAliasDeclaration(statement)) && statement.name) this.inputTypes.set(statement.name.text,statement);
      if (this.ts.isImportDeclaration(statement)) this.indexImport(statement);
      if (this.ts.isClassDeclaration(statement) && statement.name) this.classes.set(statement.name.text, statement);
      if (this.ts.isFunctionDeclaration(statement) && statement.name && statement.body) this.functions.set(statement.name.text, statement);
      if (this.ts.isVariableStatement(statement)) for (const declaration of statement.declarationList.declarations) this.indexValue(declaration.name, declaration.initializer);
    }
    this.indexBindings();
    const flow = this.selectFlow();
    if (!flow) return this.result();
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
    this.diagnoseResourceMutations();
    this.analyzeRegistration(this.method(flow,"getSteps"));
    this.validateClassCustomization();
    for (const [stepName,nodeID] of this.registered) {
      const connector = this.connectorSteps.get(stepName);
      if (connector) { this.uses.push(this.factoryResult(nodeID,connector)); continue; }
      const instance = this.stepInstances.get(stepName);
      this.currentFrame = {instance};
      this.analyzeStep(stepName,nodeID,instance.declaration);
      // Single-Flow graphs do not draw Step locks; the locked-method check still needs them.
      const lockEdges = this.stepLockEdges(nodeID,instance.declaration);
      if (this.scopesFlowResources) this.graph.edges.push(...lockEdges);
      else this.undrawnLockEdges.push(...lockEdges);
      this.lockedMethodCandidates.push({nodeID,stepName,instance});
      this.currentFrame = {};
    }
    this.analyzeRPCs(flow);
    this.recordMethodExternalCalls();
    this.analyzeTimeoutHandler(flow);
    this.analyzeTriggerBindings(this.method(flow,"getConnectorTriggerBindings"));
    if (this.scopesFlowResources) this.persistenceResourceIds = this.persistenceResources(flow);
    return this.result();
  }

  persistenceResources(flow) {
    const pending = [];
    this.walkStatements(this.handlerStatements(this.method(flow,"getPersistenceSchema")),"",expression => {
      pending.push(expression);
      return true;
    });
    const seen = new Set();
    const registered = new Set();
    while (pending.length) {
      const {node} = this.resolvedValue(pending.pop(),{});
      if (!node || seen.has(node)) continue;
      seen.add(node);
      const resourceID = this.resourceNodes.get(node);
      if (resourceID) registered.add(resourceID);
      else if (this.ts.isArrayLiteralExpression(node)) pending.push(...node.elements);
      else if (this.ts.isSpreadElement(node) || this.ts.isSpreadAssignment(node)) pending.push(node.expression);
      else if (this.ts.isConditionalExpression(node)) pending.push(node.whenTrue,node.whenFalse);
      else if (this.ts.isObjectLiteralExpression(node)) {
        for (const property of node.properties) {
          if (this.ts.isSpreadAssignment(property)) pending.push(property.expression);
          else if (property.name && ["attributes","channels","streams"].includes(this.propertyName(property.name))) pending.push(this.propertyValueExpression(property));
        }
      }
    }
    return [...registered];
  }

  propertyValueExpression(property) {
    if (this.ts.isPropertyAssignment(property)) return property.initializer;
    if (this.ts.isShorthandPropertyAssignment(property)) return property.name;
    return undefined;
  }

  selectFlow() {
    const flows = [...this.classes.values()].filter(declaration => this.method(declaration,"getSteps"));
    this.scopesFlowResources = flows.length > 1;
    if (!this.flowType && flows.length === 1) return flows[0];
    if (!this.flowType && !flows.length) {
      this.error("flow_not_found","source must define exactly one Flow");
      return undefined;
    }
    const candidates = [];
    const matches = [];
    for (const declaration of flows) {
      const name = this.candidateFlowType(declaration);
      candidates.push(name);
      if (name === this.flowType) matches.push(declaration);
    }
    if (matches.length === 1) return matches[0];
    const available = candidates.join(", ") || "(none)";
    const requested = JSON.stringify(this.flowType);
    if (!this.flowType) this.error("multiple_flows",`source defines multiple Flows; use --flow with one of: ${available}`);
    else if (matches.length) this.error("multiple_flows",`multiple Flows have type ${requested}; available Flow types: ${available}`);
    else this.error("flow_not_found",`Flow type ${requested} was not found; available Flow types: ${available}`);
    for (const declaration of flows) this.graph.nodes.push({id:`unknown:flow:${declaration.name.text}`,kind:"unknown",name:declaration.name.text,span:this.span(declaration)});
    return undefined;
  }

  candidateFlowType(declaration) {
    if (!this.method(declaration,"getFlowType")) return declaration.name.text;
    // Resolving `this` fields requires escape analysis for the candidate's class.
    const {dynamicClasses,hasDynamicFields,dynamicFieldsCause} = this;
    this.dynamicClasses = new Set(dynamicClasses);
    this.flowClass = declaration;
    this.indexEscapes();
    const name = this.typeName(declaration,"getFlowType",false);
    this.dynamicClasses = dynamicClasses;
    this.hasDynamicFields = hasDynamicFields;
    this.dynamicFieldsCause = dynamicFieldsCause;
    return name;
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
    // syncToAttributeStore and hardDeleteOnNull return the Attribute they configure.
    // plaintext returns a copy with the same name, codec, and index.
    // Lock requirement options also return a copy with the same name and codec.
    const configuredBy = [];
    while (expression && this.ts.isCallExpression(expression) && this.ts.isPropertyAccessExpression(expression.expression) && definitionOptionMethods.has(expression.expression.name.text)) {
      configuredBy.push(expression);
      expression = this.unwrap(expression.expression.expression);
    }
    if (!expression || !this.ts.isNewExpression(expression)) return;
    const constructor = this.dexName(expression.expression);
    const kind = {Attribute:"attribute",AttributeMap:"attribute",Channel:"channel",ChannelMap:"channel",Join:"channel",Stream:"stream"}[constructor];
    if (!kind) return;
    const optionMethods = definitionOptionMethodsByConstructor[constructor];
    if (configuredBy.some(call => !optionMethods?.has(call.expression.name.text))) return;
    let resourceName = this.staticLiteral(expression.arguments?.[0]);
    const nodeID = `resource:${kind}:${name}`;
    if (typeof resourceName !== "string") {
      this.resourceError(nodeID,"dynamic_resource_name",`resource ${name} must use a static name`,expression);
      if (!this.scopesFlowResources) return;
      resourceName = name;
    }
    this.resourceNodes.set(expression,nodeID);
    for (const call of configuredBy) this.resourceNodes.set(call,nodeID);
    // A Join is a ChannelMap of branch results keyed by round.
    const isJoin = constructor === "Join";
    if (isJoin) this.joinResourceIDs.add(nodeID);
    if (kind === "attribute") this.attributeDeclarations.set(nodeID,expression);
    this.graph.nodes.push({id:nodeID,kind,name:resourceName,span:this.span(expression),resource:{valueType:this.resourceValueType(expression),map:isJoin || constructor.endsWith("Map")}});
  }

  // A call on a resource is drawn as that resource, so changing a resource's members, prototype, or class is blocked.
  diagnoseResourceMutations() {
    const kinds = this.ts.SyntaxKind;
    this.walk(this.tree,node => {
      const base = this.ts.isClassLike(node) ? this.dexName(this.extendsClause(node)?.expression) : undefined;
      if (resourceClassNames.has(base)) this.error("dynamic_resource_access",`class ${node.name?.text || "expression"} extends ${base}; create resources with new ${base}(...) directly`,node);
      let target;
      if (this.ts.isBinaryExpression(node) && node.operatorToken.kind >= kinds.FirstAssignment && node.operatorToken.kind <= kinds.LastAssignment) target = node.left;
      else if ((this.ts.isPrefixUnaryExpression(node) || this.ts.isPostfixUnaryExpression(node)) && [kinds.PlusPlusToken,kinds.MinusMinusToken].includes(node.operator)) target = node.operand;
      else if (this.ts.isDeleteExpression(node)) target = node.expression;
      target = this.unwrap(target);
      if (target && (this.ts.isPropertyAccessExpression(target) || this.ts.isElementAccessExpression(target))) {
        const resource = this.resourceObject(target.expression);
        if (resource) this.resourceError(resource,"dynamic_resource_access",`${this.lineText(node)} changes a member of a resource; resource methods must stay as Dex defines them`,node);
      }
      if (this.ts.isCallExpression(node) && reflectiveWriteNames.has(this.unwrap(node.expression)?.getText(this.tree))) {
        const resource = this.resourceObject(node.arguments[0]);
        if (resource) this.resourceError(resource,"dynamic_resource_access",`${this.lineText(node)} changes a resource or its prototype through reflection`,node);
      }
    });
  }

  // A resource object, a Dex resource class prototype, or the prototype of a resource object.
  resourceObject(expression) {
    const value = this.unwrap(expression);
    if (!value) return false;
    const resourceID = this.resourceNodes.get(this.resolvedValue(value,{}).node);
    if (resourceID) return resourceID;
    if (this.ts.isPropertyAccessExpression(value) && ["prototype","__proto__"].includes(value.name.text)) return resourceClassNames.has(this.dexName(value.expression)) || this.resourceObject(value.expression);
    const callee = this.ts.isCallExpression(value) ? this.unwrap(value.expression)?.getText(this.tree) : undefined;
    return (callee === "Object.getPrototypeOf" || callee === "Reflect.getPrototypeOf") && this.resourceObject(value.arguments[0]);
  }

  resourceError(resource,code,message,node) {
    const diagnosticIndex = this.graph.diagnostics.length;
    this.error(code,message,node);
    if (this.scopesFlowResources && typeof resource === "string" && this.graph.diagnostics.length > diagnosticIndex) this.diagnosticResourceIds[diagnosticIndex] = resource;
  }

  resourceValueType(expression) {
    const annotation = this.resourceTypeAnnotation(expression);
    if (annotation) return annotation.getText(this.tree);
    const codec = this.unwrap(expression.arguments?.[1]);
    return (codec && codecValueTypes[this.dexName(codec)]) || "unknown";
  }

  resourceTypeAnnotation(expression) {
    if (expression.typeArguments?.length) return expression.typeArguments[0];
    const codec = this.unwrap(expression.arguments?.[1]);
    if (codec && this.ts.isCallExpression(codec) && this.dexName(codec.expression) === "jsonCodec") return codec.typeArguments?.[0];
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
        if (isStart) {
          starts++;
          this.graph.flow.startStepId = `step:${target}`;
          if (!this.connectorSteps.has(target)) this.startInput = this.startInputFact(target,declaration,argument);
        }
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
    else {
      this.analyzeDecisions(nodeID,execute);
      this.analyzeResourceAccess(nodeID,execute,"execute",declaration);
    }
    const waitFor = this.method(declaration,"waitFor");
    if (waitFor) this.analyzeWaitFor(nodeID,stepName,waitFor,declaration);
    const options = this.method(declaration,"getStepOptions");
    if (options) this.analyzeExecuteFailure(nodeID,options);
  }

  analyzeTimeoutHandler(flow) {
    const handler = this.method(flow,"handleTimeout");
    if (!handler) return;
    const nodeID = `timeout_handler:${flow.name.text}`;
    this.graph.nodes.push({id:nodeID,kind:"timeout_handler",name:"handleTimeout",span:this.span(handler)});
    if (!handler.body) return;
    this.analyzeDecisions(nodeID,handler,"timeout");
    this.analyzeResourceAccess(nodeID,handler,"timeout",flow);
  }

  analyzeWaitFor(ownerID,stepType,handler,declaration) {
    const returns = [];
    const hiddenWaits = [];
    this.walkStatements(this.handlerStatements(handler),"",(expression,condition) => {
      const found = this.waitOutcomes(expression,condition,hiddenWaits);
      returns.push(...found);
      return found.length > 0;
    });
    for (const hidden of hiddenWaits) this.addHiddenWait(ownerID,stepType,hidden);
    if (!returns.length && !hiddenWaits.length) {
      if (!this.onlyThrows(handler)) { this.addHiddenWait(ownerID,stepType,handler); return; }
      const span = this.span(handler);
      this.graph.nodes.push({id:`wait:${stepType}:${span.startLine}:${span.startColumn}`,kind:"wait",name:"failure",parentId:ownerID,phase:"wait_for",span,wait:{type:"failure",conditions:[]}});
    }
    if (returns.length > 1) this.graph.nodes.push({id:`wait-dispatch:${stepType}`,kind:"wait_dispatch",name:"WaitFor",parentId:ownerID,phase:"wait_for",span:this.span(handler)});
    for (const {call,condition,occurrence} of returns) {
      const span = this.span(call);
      // A Wait bound to a name can be returned several times; each return is its own Wait node.
      const returned = this.span(occurrence);
      const waitID = `wait:${stepType}:${returned.startLine}:${returned.startColumn}`;
      const type = this.waitType(call);
      this.graph.nodes.push({id:waitID,kind:"wait",name:type,parentId:ownerID,condition,phase:"wait_for",span,wait:{type,conditions:this.waitConditions(waitID,call)}});
    }
    this.analyzeResourceAccess(ownerID,handler,"wait_for",declaration);
  }

  waitOutcomes(expression,condition,hiddenWaits) {
    expression = this.unwrap(expression);
    if (!expression) return [];
    if (this.ts.isConditionalExpression(expression)) {
      const branch = expression.condition.getText(this.tree);
      return [
        ...this.waitOutcomes(expression.whenTrue,this.combine(condition,branch),hiddenWaits),
        ...this.waitOutcomes(expression.whenFalse,this.combine(condition,this.negate(branch)),hiddenWaits),
      ];
    }
    const call = this.ts.isIdentifier(expression) ? this.returnedWaitConstInitializer(expression) : expression;
    if (call && this.ts.isCallExpression(call) && this.waitType(call)) return [{call,condition,occurrence:expression}];
    hiddenWaits.push(expression);
    return [];
  }

  // Supported: a const declared directly in the waitFor body whose every reference is a returned value.
  returnedWaitConstInitializer(identifier) {
    const declaration = this.localBindingDeclaration(identifier);
    const statement = declaration?.parent?.parent;
    if (!declaration || !this.ts.isVariableDeclaration(declaration) || !declaration.initializer || !statement || !this.ts.isVariableStatement(statement)) return undefined;
    if (!(statement.declarationList.flags & this.ts.NodeFlags.Const) || !this.ts.isBlock(statement.parent) || !this.ts.isFunctionLike(statement.parent.parent)) return undefined;
    const isReturnedOnly = this.valueReferences(statement.parent,declaration).every(reference => {
      let use = reference;
      while (this.isTransparentWrapper(use.parent) || (this.ts.isConditionalExpression(use.parent) && use.parent.condition !== use)) use = use.parent;
      return this.ts.isReturnStatement(use.parent);
    });
    return isReturnedOnly ? this.unwrap(declaration.initializer) : undefined;
  }

  // Returns the identifiers in scope that read the value of declaration; property names and keys are not reads.
  valueReferences(scope,declaration) {
    const references = [];
    this.walk(scope,node => {
      if (!this.ts.isIdentifier(node) || node === declaration.name || node.text !== declaration.name.text || !this.isValueReference(node)) return;
      if (this.localBindingDeclaration(node) === declaration) references.push(node);
    });
    return references;
  }

  addHiddenWait(ownerID,stepType,node) {
    const span = this.span(node);
    this.graph.nodes.push({id:`unknown:wait:${stepType}:${span.startLine}:${span.startColumn}`,kind:"unknown",name:"Dynamic WaitFor",parentId:ownerID,span});
    this.error("hidden_dex_wait",`waitFor of Step ${stepType} must directly return a Wait factory call such as Wait.until, Wait.anyOf, or Wait.skipImmediately (${this.lineText(node)})`,node);
  }

  // A waitFor that only throws always fails, which the graph shows as a failure Wait.
  onlyThrows(handler) {
    let hasThrow = false;
    let hasReturn = false;
    const visit = node => {
      if (node !== handler && this.ts.isFunctionLike(node)) return;
      if (this.ts.isThrowStatement(node)) hasThrow = true;
      if (this.ts.isReturnStatement(node)) hasReturn = true;
      this.ts.forEachChild(node,visit);
    };
    visit(handler);
    return hasThrow && !hasReturn;
  }

  waitType(call) {
    const callee = this.unwrap(call.expression);
    if (!callee || !this.ts.isPropertyAccessExpression(callee) || this.dexName(callee.expression) !== "Wait") return undefined;
    return waitTypes.get(callee.name.text);
  }

  waitConditions(waitID,call) {
    const conditions = [];
    const acceptsSubFlowSpread = ["allOf","anyOf"].includes(this.waitType(call));
    for (const [index,argument] of call.arguments.entries()) this.collectWaitCondition(waitID,argument,index,conditions,acceptsSubFlowSpread);
    return conditions;
  }

  // Each argument is one condition at its index; ConditionCombination.of numbers its own arguments, as Go's Combo does.
  collectWaitCondition(waitID,argument,index,conditions,acceptsSubFlowSpread = false) {
    if (this.ts.isSpreadElement(argument)) {
      // A spread SubFlow.run map is one SubFlow condition, as in the Python analyzer.
      const subFlowRun = acceptsSubFlowSpread ? this.mappedSubFlowRun(argument.expression) : undefined;
      if (subFlowRun) this.collectWaitCondition(waitID,subFlowRun,index,conditions);
      else this.addUnknownWaitCondition(argument,index,conditions);
      return;
    }
    // Supported: the condition call written directly as the argument.
    const call = this.unwrap(argument);
    const callee = call && this.ts.isCallExpression(call) ? this.unwrap(call.expression) : undefined;
    if (!callee || !this.ts.isPropertyAccessExpression(callee)) { this.addUnknownWaitCondition(argument,index,conditions); return; }
    const owner = this.dexName(callee.expression);
    const method = callee.name.text;
    const span = this.span(call);
    if (owner === "ConditionCombination" && method === "of") {
      for (const [combinationIndex,combined] of call.arguments.entries()) this.collectWaitCondition(waitID,combined,combinationIndex,conditions);
      return;
    }
    if (owner === "Timer" && method === "byDuration") {
      const duration = call.arguments[0];
      const expression = duration ? duration.getText(this.tree) : "duration";
      conditions.push({index,kind:"timer",label:`${this.humanizeMilliseconds(duration) ?? expression} timer`,expression,span});
      return;
    }
    if (owner === "SubFlow" && method === "run") {
      const target = call.arguments[0] && this.resolvedValue(call.arguments[0],this.currentFrame).node;
      const named = target && this.ts.isNewExpression(target) ? this.unwrap(target.expression) : target;
      const name = named ? named.getText(this.tree) : "SubFlow";
      const subFlowID = `subflow:${waitID}:${index}:${name}:${span.startLine}:${span.startColumn}`;
      this.graph.nodes.push({id:subFlowID,kind:"subflow",name,external:true,span});
      this.graph.edges.push({kind:"subflow",from:waitID,to:subFlowID,label:"start",span,metadata:{conditionIndex:index}});
      conditions.push({index,kind:"subflow",label:name,subFlowId:subFlowID,span});
      return;
    }
    const resourceID = channelConditionMethods.has(method) ? this.accessedResource(this.unwrap(callee.expression)) : undefined;
    if (!resourceID?.startsWith("resource:channel:")) { this.addUnknownWaitCondition(argument,index,conditions); return; }
    const {label,expression} = this.channelConditionLabel(resourceID,method,call.arguments);
    conditions.push({index,kind:"channel",label,resourceId:resourceID,...(expression ? {expression} : {}),span});
    this.graph.edges.push({kind:"wait_condition",from:resourceID,to:waitID,label,span,metadata:{conditionIndex:index}});
  }

  // Returns the SubFlow.run call of items.map(item => SubFlow.run(flow, item)) when flow is the same for every item.
  // The analyzer has no type checker, so it accepts only one conservative shape and leaves the rest to dynamic_wait_condition.
  mappedSubFlowRun(expression) {
    const mapCall = this.stripTypeWrappers(expression);
    if (!mapCall || !this.ts.isCallExpression(mapCall) || this.hasOptionalChain(mapCall) || mapCall.arguments.length !== 1) return undefined;
    const mapCallee = this.stripTypeWrappers(mapCall.expression);
    if (!this.ts.isPropertyAccessExpression(mapCallee) || mapCallee.name.text !== "map" || !this.isArrayParameter(mapCallee.expression)) return undefined;
    const callback = this.stripTypeWrappers(mapCall.arguments[0]);
    if (!callback || !(this.ts.isArrowFunction(callback) || this.ts.isFunctionExpression(callback))) return undefined;
    // An async callback returns a Promise and a generator returns an iterator, not a condition.
    if (callback.asteriskToken || callback.modifiers?.some(modifier => modifier.kind === this.ts.SyntaxKind.AsyncKeyword)) return undefined;
    if (!callback.parameters.every(parameter => this.ts.isIdentifier(parameter.name) && !parameter.initializer && !parameter.dotDotDotToken)) return undefined;
    const statements = this.handlerStatements(callback);
    if (statements.length !== 1 || !this.ts.isReturnStatement(statements[0])) return undefined;
    const run = this.stripTypeWrappers(statements[0].expression);
    if (!run || !this.ts.isCallExpression(run) || this.hasOptionalChain(run)) return undefined;
    const runCallee = this.stripTypeWrappers(run.expression);
    if (!this.ts.isPropertyAccessExpression(runCallee) || this.dexName(runCallee.expression) !== "SubFlow" || runCallee.name.text !== "run") return undefined;
    const target = run.arguments[0];
    if (!target || this.ts.isSpreadElement(target) || this.hasOptionalChain(target) || !this.isFixedFlowReference(this.stripTypeWrappers(target),callback)) return undefined;
    return run;
  }

  stripTypeWrappers(expression) {
    while (expression && (this.ts.isParenthesizedExpression(expression) || this.ts.isAsExpression(expression) || this.ts.isSatisfiesExpression(expression) || this.ts.isNonNullExpression(expression) || this.ts.isTypeAssertionExpression(expression))) expression = expression.expression;
    return expression;
  }

  // Parentheses end optional-chain propagation, so every link of the chain is checked.
  hasOptionalChain(expression) {
    for (let node = this.stripTypeWrappers(expression); node; node = this.stripTypeWrappers(node.expression)) {
      if (!(this.ts.isPropertyAccessExpression(node) || this.ts.isElementAccessExpression(node) || this.ts.isCallExpression(node))) return false;
      if (node.questionDotToken) return true;
    }
    return false;
  }

  // Supported: a never-reassigned parameter whose type annotation is an array, readonly array, or tuple type.
  isArrayParameter(receiver) {
    receiver = this.stripTypeWrappers(receiver);
    if (!receiver || !this.ts.isIdentifier(receiver)) return false;
    const declaration = this.localBindingDeclaration(receiver);
    if (!declaration || !this.ts.isParameter(declaration) || !declaration.type || !this.ts.isIdentifier(declaration.name)) return false;
    let type = declaration.type;
    if (this.ts.isTypeOperatorNode(type) && type.operator === this.ts.SyntaxKind.ReadonlyKeyword) type = type.type;
    if (!this.isArrayTypeNode(type) && !this.ts.isTupleTypeNode(type)) return false;
    return this.staticBinding(declaration.parent,receiver.text) === declaration;
  }

  // Supported: an import, a module const of a Flow reference, or this.field in an arrow callback.
  isFixedFlowReference(target,callback) {
    if (this.ts.isIdentifier(target)) return this.isModuleFlowReference(target,new Set());
    if (!this.ts.isPropertyAccessExpression(target) || !this.ts.isArrowFunction(callback)) return false;
    const owner = this.selfReferenceClass(this.stripTypeWrappers(target.expression));
    return !!owner && this.isReadonlyFlowField(owner,target.name.text);
  }

  isModuleFlowReference(identifier,seen) {
    if (seen.has(identifier.text) || this.bindingScope(identifier) !== this.tree) return false;
    seen.add(identifier.text);
    const declaration = this.staticBinding(this.tree,identifier.text);
    if (!declaration) return false;
    if (this.ts.isImportSpecifier(declaration) || this.ts.isImportClause(declaration)) return !declaration.isTypeOnly;
    const statement = declaration.parent?.parent;
    if (!this.ts.isVariableDeclaration(declaration) || !statement || !this.ts.isVariableStatement(statement) || statement.parent !== this.tree) return false;
    if (!(statement.declarationList.flags & this.ts.NodeFlags.Const) || !this.ts.isIdentifier(declaration.name)) return false;
    return this.isFlowValueExpression(declaration.initializer,seen);
  }

  // Supported: new of a Flow class declared in this file or of an import, or a module Flow reference.
  isFlowValueExpression(expression,seen) {
    const value = this.stripTypeWrappers(expression);
    if (!value) return false;
    if (this.ts.isIdentifier(value)) return this.isModuleFlowReference(value,seen);
    if (!this.ts.isNewExpression(value) || !this.ts.isIdentifier(value.expression)) return false;
    const flowClass = this.classes.get(value.expression.text);
    if (flowClass && this.bindingScope(value.expression) === this.tree) return !!this.method(flowClass,"getSteps");
    const declaration = this.bindingScope(value.expression) === this.tree ? this.staticBinding(this.tree,value.expression.text) : undefined;
    return !!declaration && (this.ts.isImportSpecifier(declaration) || this.ts.isImportClause(declaration));
  }

  // Supported: a readonly parameter property typed as a Flow class of this file, or a readonly field initialized to a Flow value.
  isReadonlyFlowField(owner,name) {
    if (this.hasDynamicFields || this.dynamicClasses.has(owner) || this.thisStores.get(owner)?.has(name)) return false;
    const isReadonly = node => !!node.modifiers?.some(modifier => modifier.kind === this.ts.SyntaxKind.ReadonlyKeyword);
    for (const member of owner.members) {
      if (this.ts.isConstructorDeclaration(member)) {
        const parameter = member.parameters.find(candidate => this.ts.isIdentifier(candidate.name) && candidate.name.text === name);
        if (!parameter) continue;
        const type = parameter.type;
        const flowClass = type && this.ts.isTypeReferenceNode(type) && this.ts.isIdentifier(type.typeName) ? this.classes.get(type.typeName.text) : undefined;
        return isReadonly(parameter) && !!flowClass && !!this.method(flowClass,"getSteps");
      }
      if (this.ts.isPropertyDeclaration(member) && member.name && this.propertyName(member.name) === name && !this.isStatic(member)) {
        return isReadonly(member) && this.isFlowValueExpression(member.initializer,new Set());
      }
    }
    return false;
  }

  addUnknownWaitCondition(node,index,conditions) {
    const text = node.getText(this.tree);
    conditions.push({index,kind:"unknown",label:text,expression:text,span:this.span(node)});
    this.error("dynamic_wait_condition",`Wait condition ${this.lineText(node)} must directly call Timer.byDuration, SubFlow.run, ConditionCombination.of, or a condition method such as forOne on a Channel, ChannelMap, or Join declared in this file`,node);
  }

  // Labels follow the Go analyzer; a map or Join condition names its instance or round first.
  channelConditionLabel(resourceID,method,args) {
    const resource = this.graph.nodes.find(node => node.id === resourceID);
    const texts = args.map(argument => argument.getText(this.tree));
    const isMap = !!resource.resource?.map && texts.length > 0;
    const counts = isMap ? texts.slice(1) : texts;
    const count = (position,fallback) => counts[position] === undefined || counts[position] === "undefined" ? fallback : counts[position];
    const suffix = {
      forAllBranches:() => "for all branches",
      forFirstBranch:() => "for first branch",
      forBranches:() => `for ${count(0,"N")} branches`,
      forRemainingBranches:() => `for remaining branches after ${count(0,"N")}`,
      forOne:() => "for 1",
      forN:() => `for ${count(0,"N")}`,
      atLeast:() => `at least ${count(0,"N")}`,
      atMost:() => `at most ${count(0,"N")}`,
      range:() => `for ${count(0,"0")}…${count(1,"∞")}`,
    }[method]();
    return {label:`${resource.name}${isMap ? `[${texts[0]}]` : ""}.${suffix}`,expression:texts.join(", ")};
  }

  humanizeMilliseconds(expression) {
    const value = this.staticNumber(expression);
    if (value === undefined || !Number.isSafeInteger(value) || value < 0) return undefined;
    for (const [size,unit] of [[86_400_000,"day"],[3_600_000,"hour"],[60_000,"minute"],[1_000,"second"],[1,"millisecond"]]) {
      if (value % size !== 0 || (value === 0 && size !== 1)) continue;
      const amount = value / size;
      return `${amount} ${unit}${amount === 1 ? "" : "s"}`;
    }
    return undefined;
  }

  staticNumber(expression) {
    const node = expression && this.resolvedValue(expression,this.currentFrame).node;
    if (!node) return undefined;
    if (this.ts.isNumericLiteral(node)) return Number(node.text);
    if (this.ts.isPrefixUnaryExpression(node) && node.operator === this.ts.SyntaxKind.MinusToken) {
      const operand = this.staticNumber(node.operand);
      return operand === undefined ? undefined : -operand;
    }
    if (!this.ts.isBinaryExpression(node)) return undefined;
    const left = this.staticNumber(node.left);
    const right = this.staticNumber(node.right);
    if (left === undefined || right === undefined) return undefined;
    const kinds = this.ts.SyntaxKind;
    return {[kinds.PlusToken]:left + right,[kinds.MinusToken]:left - right,[kinds.AsteriskToken]:left * right,[kinds.SlashToken]:left / right}[node.operatorToken.kind];
  }

  // Records the resource reads, writes, and publishes of a handler and of the file's helpers it calls.
  analyzeResourceAccess(ownerID,handler,phase,ownerClass,viewRPCName) {
    const accesses = [];
    this.collectResourceAccesses(handler,ownerClass,"",new Set([handler]),accesses,0);
    const seen = new Set();
    for (const isHelperAccess of [false,true]) {
      for (const access of accesses) {
        if (!!access.helper !== isHelperAccess) continue;
        this.diagnoseStreamOutsideStep(ownerID,phase,access);
        if (viewRPCName && ["set","delete","publish","deleteChannelMessage"].includes(access.method)) {
          this.error("v2_view_rpc_side_effect",`${viewRPCName} must be read-only`,access.call);
        }
        const kind = this.resourceEdgeKind(access.method,phase);
        const key = `${kind}:${access.resourceID}:${access.method}`;
        if (!kind || kind === "wait_condition" || seen.has(key)) continue;
        seen.add(key);
        const metadata = {phase};
        if (access.helper) Object.assign(metadata,{via:"helper",function:access.helper});
        if (access.resourceID.startsWith("resource:stream:") && access.method === "write") Object.assign(metadata,{bestEffort:true,repeatable:true,role:"progress"});
        const isRead = kind === "resource_read";
        this.graph.edges.push({kind,from:isRead ? access.resourceID : ownerID,to:isRead ? ownerID : access.resourceID,label:access.method,span:this.span(access.call),metadata});
      }
    }
  }

  // Supported receivers: a resource reference, or a parameter of a followed helper bound to one.
  // A resource operation inside a nested function, a helper used as a value, and .call/.apply/.bind are blocked.
  collectResourceAccesses(handler,ownerClass,helperName,callChain,accesses,depth,bindings=new Map()) {
    const classify = expression => {
      const value = this.unwrap(expression);
      if (value && this.ts.isIdentifier(value) && bindings.has(value.text) && this.localBindingDeclaration(value)?.parent === handler) {
        const resourceID = bindings.get(value.text);
        return resourceID ? {resourceID} : {};
      }
      const resourceID = this.accessedResource(value);
      if (resourceID) return {resourceID};
      return {isAlias:!!this.resourceFor(value),isNotResource:this.isProvablyNotResource(value)};
    };
    const streamWriters = this.streamWriters(handler,classify);
    const blockNested = (node,what) => this.blockResourceAccess(node,`${what} inside a nested function, which may run later or never`);
    this.walkReachable(handler.body,node => {
      const isNested = this.isInNestedFunction(node,handler);
      if (this.isHelperValue(node,ownerClass)) {
        this.blockResourceAccess(node,"uses a helper as a value; call it directly");
        return;
      }
      if (!this.ts.isCallExpression(node)) return;
      const callee = this.unwrap(node.expression);
      if (callee && this.ts.isPropertyAccessExpression(callee) && ["call","apply","bind"].includes(callee.name.text)) {
        const target = this.unwrap(callee.expression);
        if (target && (this.ts.isPropertyAccessExpression(target) || this.ts.isElementAccessExpression(target) || this.functions.has(target.getText(this.tree)))) {
          this.blockResourceAccess(node,`calls ${callee.name.text} on a method or function`);
          return;
        }
      }
      const helper = this.resourceHelper(callee,ownerClass);
      const member = helper ? undefined : this.memberCall(callee);
      if (member) {
        const receiver = this.unwrap(member.receiver);
        const writer = receiver && this.ts.isIdentifier(receiver) ? streamWriters.get(this.localBindingDeclaration(receiver)) : undefined;
        if (writer) {
          if (isNested) blockNested(node,"writes a Stream writer");
          else if (member.method === undefined) this.blockResourceAccess(callee,"calls a Stream writer method through a computed key");
          else if (member.method === "write") accesses.push({call:node,method:"write",resourceID:writer,helper:helperName});
          return;
        }
        const target = classify(receiver);
        if (target.resourceID) {
          if (isNested) blockNested(node,"accesses a resource");
          else if (member.method === undefined) this.blockResourceAccess(callee,"calls a resource method through a computed key");
          else accesses.push({call:node,method:member.method,resourceID:target.resourceID,helper:helperName});
          return;
        }
        if (target.isAlias) {
          this.blockResourceAccess(node,"calls a method on a local alias of a resource");
          return;
        }
        const isResourceMethod = member.method === undefined || resourceMethodNames.has(member.method);
        if (!target.isNotResource && isResourceMethod && node.arguments.some(argument => this.isDexContextValue(argument))) {
          this.blockResourceAccess(node,`calls ${member.method ?? "a computed method"} with the Dex Context on a receiver that is not provably a resource declared in this file`);
          return;
        }
      }
      if (helper) {
        if (callChain.has(helper.declaration)) { this.blockResourceAccess(node,`calls helper ${helper.name} recursively`); return; }
        if (depth >= maximumHelperDepth) { this.blockResourceAccess(node,`reaches helper ${helper.name} more than ${maximumHelperDepth} calls deep`); return; }
        if (!this.ts.isFunctionDeclaration(helper.declaration) && !this.ts.isMethodDeclaration(helper.declaration)) { this.blockResourceAccess(node,`calls ${helper.name}, an arrow function or function expression field; declare it as a method`); return; }
        const writtenParameter = helper.declaration.parameters.find(parameter => !this.ts.isIdentifier(parameter.name) || this.identifierWrites.some(write => write.name === parameter.name.text && this.isWithin(write.node,helper.declaration)));
        if (writtenParameter) { this.blockResourceAccess(node,`calls helper ${helper.name}, which destructures or reassigns parameter ${writtenParameter.name.getText(this.tree)}`); return; }
        const helperBindings = new Map(helper.declaration.parameters.map((parameter,index) => [parameter.name.text,node.arguments[index] ? classify(node.arguments[index]).resourceID : undefined]));
        const helperAccesses = [];
        // As in Go, an access names the helper that contains it.
        this.collectResourceAccesses(helper.declaration,ownerClass,helper.name,new Set([...callChain,helper.declaration]),helperAccesses,depth + 1,helperBindings);
        if (isNested && helperAccesses.length) blockNested(node,`calls helper ${helper.name}, which accesses a resource,`);
        else accesses.push(...helperAccesses);
        return;
      }
      if (this.isDexCall(callee)) return;
      for (const argument of node.arguments) {
        const target = classify(argument);
        if (target.resourceID || target.isAlias) this.blockResourceAccess(argument,`is a resource passed to ${callee ? callee.getText(this.tree) : "a call"}, which the analyzer does not follow`);
      }
    });
  }

  isInNestedFunction(node,handler) {
    for (let current = node.parent; current && current !== handler; current = current.parent) if (this.ts.isFunctionLike(current)) return true;
    return false;
  }

  // A helper reference that is read as a value, such as passed as a callback or followed by .call, instead of called.
  isHelperValue(node,ownerClass) {
    if (!this.ts.isIdentifier(node) && !this.ts.isPropertyAccessExpression(node)) return false;
    if (this.ts.isIdentifier(node) && (!this.isValueReference(node) || (this.ts.isPropertyAccessExpression(node.parent) && node.parent.name === node))) return false;
    let use = node;
    while (this.isTransparentWrapper(use.parent)) use = use.parent;
    if (this.ts.isCallExpression(use.parent) && use.parent.expression === use) return false;
    if (this.ts.isPropertyAccessExpression(use.parent) && use.parent.expression === use && !["call","apply","bind"].includes(use.parent.name.text)) return false;
    return !!this.resourceHelper(node,ownerClass);
  }

  // Supported: const writer = stream.bufferedText(...), then writer.method(...) calls. Any other use is blocked.
  streamWriters(handler,classify) {
    const writers = new Map();
    this.walkReachable(handler.body,node => {
      if (!this.ts.isCallExpression(node) || !this.ts.isPropertyAccessExpression(node.expression) || node.expression.name.text !== "bufferedText") return;
      if (!classify(node.expression.expression).resourceID?.startsWith("resource:stream:")) return;
      let declaration = node.parent;
      while (this.isTransparentWrapper(declaration)) declaration = declaration.parent;
      const isConst = this.ts.isVariableDeclaration(declaration) && this.ts.isIdentifier(declaration.name) && !!(declaration.parent.flags & this.ts.NodeFlags.Const);
      if (!isConst) {
        if (!this.ts.isExpressionStatement(declaration)) this.blockResourceAccess(node,"is a Stream writer that is not bound to a const");
        return;
      }
      writers.set(declaration,classify(node.expression.expression).resourceID);
    });
    for (const declaration of writers.keys()) {
      for (const node of this.valueReferences(handler.body,declaration)) {
        const access = node.parent;
        const isCalled = (this.ts.isPropertyAccessExpression(access) || this.ts.isElementAccessExpression(access)) && access.expression === node && this.ts.isCallExpression(access.parent) && access.parent.expression === access;
        if (!isCalled) this.blockResourceAccess(node,"is a Stream writer used other than by calling one of its methods");
      }
    }
    return writers;
  }

  blockResourceAccess(node,reason) {
    this.error("dynamic_resource_access",`${this.lineText(node)} ${reason}; access each resource directly by its module const or Flow field, or through a helper in this file`,node);
  }

  // A literal, a function, a non-resource Dex import, a Dex Context, or a new instance of a listed global builtin class.
  isProvablyNotResource(expression) {
    const value = expression && this.resolvedValue(expression,this.currentFrame).node;
    if (!value) return false;
    const kinds = this.ts.SyntaxKind;
    if (this.ts.isLiteralExpression(value) || this.ts.isArrayLiteralExpression(value) || this.ts.isObjectLiteralExpression(value) || this.ts.isFunctionLike(value) || [kinds.TrueKeyword,kinds.FalseKeyword,kinds.NullKeyword].includes(value.kind)) return true;
    if (this.ts.isNewExpression(value)) {
      const constructor = this.unwrap(value.expression);
      return !!constructor && this.ts.isIdentifier(constructor) && builtinClassNames.has(constructor.text) && !this.bindingScope(constructor);
    }
    if (this.ts.isIdentifier(value) && this.dexName(value)) return !resourceClassNames.has(this.dexName(value));
    return this.isDexContextValue(value);
  }

  // Supported: a parameter or const typed with the Dex Context or AsyncContext, through renamed imports, type aliases, and const aliases.
  isDexContextValue(expression) {
    let value = this.unwrap(expression);
    for (let remaining = maximumHelperDepth; value && this.ts.isIdentifier(value) && remaining > 0; remaining--) {
      const declaration = this.localBindingDeclaration(value);
      if (!declaration) return false;
      if (declaration.type) return this.isDexContextType(declaration.type,new Set());
      if (!this.ts.isVariableDeclaration(declaration) || !declaration.initializer) return false;
      value = this.unwrap(declaration.initializer);
    }
    return false;
  }

  isDexContextType(typeNode,seen) {
    if (!typeNode || seen.has(typeNode)) return false;
    seen.add(typeNode);
    if (this.ts.isParenthesizedTypeNode(typeNode)) return this.isDexContextType(typeNode.type,seen);
    if (this.ts.isUnionTypeNode(typeNode) || this.ts.isIntersectionTypeNode(typeNode)) return typeNode.types.some(part => this.isDexContextType(part,seen));
    if (!this.ts.isTypeReferenceNode(typeNode)) return false;
    const name = typeNode.typeName;
    if (this.ts.isQualifiedName(name)) return this.imports.get(name.left.getText(this.tree)) === "*" && contextTypeNames.has(name.right.text);
    if (contextTypeNames.has(this.imports.get(name.text))) return true;
    const alias = this.inputTypes.get(name.text);
    return !!alias && this.ts.isTypeAliasDeclaration(alias) && this.isDexContextType(alias.type,seen);
  }

  // Returns the receiver and method of obj.method(...) or obj["method"](...); a computed key leaves method undefined.
  memberCall(callee) {
    if (!callee) return undefined;
    if (this.ts.isPropertyAccessExpression(callee)) return {receiver:callee.expression,method:callee.name.text};
    if (!this.ts.isElementAccessExpression(callee)) return undefined;
    const key = this.unwrap(callee.argumentExpression);
    const isLiteralKey = !!key && (this.ts.isStringLiteral(key) || this.ts.isNoSubstitutionTemplateLiteral(key));
    return {receiver:callee.expression,method:isLiteralKey ? key.text : undefined};
  }

  isDexCall(callee) {
    if (!callee) return false;
    if (this.dexName(callee)) return true;
    return this.ts.isPropertyAccessExpression(callee) && !!this.dexName(callee.expression);
  }

  // Supported: a module-level name, or this.field, this.flow.field, and similar this field chains that resolvedValue proves.
  isSupportedResourceReference(expression) {
    if (!expression) return false;
    if (this.ts.isIdentifier(expression)) return this.bindingScope(expression) === this.tree;
    for (let current = expression; current; current = this.unwrap(current.expression)) {
      if (current.kind === this.ts.SyntaxKind.ThisKeyword) return true;
      if (!this.ts.isPropertyAccessExpression(current)) return false;
    }
    return false;
  }

  accessedResource(receiver) {
    if (!receiver) return undefined;
    const isInstanceCall = this.ts.isCallExpression(receiver) && this.ts.isPropertyAccessExpression(receiver.expression) && receiver.expression.name.text === "instance";
    const named = this.unwrap(isInstanceCall ? receiver.expression.expression : receiver);
    if (!this.isSupportedResourceReference(named)) return undefined;
    const resourceID = this.resourceFor(named);
    return !isInstanceCall || resourceID?.startsWith("resource:attribute:") ? resourceID : undefined;
  }

  resourceHelper(callee,ownerClass) {
    if (!callee) return undefined;
    if (this.ts.isIdentifier(callee)) {
      const declaration = this.functions.get(callee.text);
      return declaration && this.bindingScope(callee) === this.tree && this.staticBinding(this.tree,callee.text) === declaration ? {declaration,name:callee.text} : undefined;
    }
    if (!this.ts.isPropertyAccessExpression(callee) || !ownerClass || this.selfReferenceClass(this.unwrap(callee.expression)) !== ownerClass) return undefined;
    const declaration = this.method(ownerClass,callee.name.text);
    return declaration?.body ? {declaration,name:callee.name.text} : undefined;
  }

  resourceEdgeKind(method,phase) {
    if (phase === "wait_for" && channelConditionMethods.has(method)) return "wait_condition";
    return resourceEdgeKinds.get(method) || "";
  }

  diagnoseStreamOutsideStep(ownerID,phase,access) {
    if ((phase !== "rpc" && phase !== "timeout") || !access.resourceID.startsWith("resource:stream:") || !["write","bufferedText"].includes(access.method)) return;
    const stream = this.graph.nodes.find(node => node.id === access.resourceID);
    const handlerLabel = phase === "rpc" ? `RPC ${ownerID.slice("rpc:".length)}` : "handleTimeout";
    const location = `${this.lineText(access.call)}${access.helper ? ` in helper ${access.helper}` : ""}`;
    this.error("step_progress_outside_step",`${handlerLabel} uses Stream "${stream.name}" (${access.method}: ${location}). Stream.write and bufferedText are only available in Step waitFor and execute. Move this Stream output to a Step; an RPC can publish a Channel message or return nextSteps to schedule it.`,access.call);
  }

  analyzeRPCs(flow) {
    for (const member of flow.members) {
      if (!this.ts.isMethodDeclaration(member) || !member.name) continue;
      const decorator = (this.ts.getDecorators?.(member) || []).find(candidate => this.isDexRPCDecorator(candidate));
      if (!decorator) continue;
      const call = this.unwrap(decorator.expression);
      this.analyzeRPC(member,this.propertyName(member.name),this.ts.isCallExpression(call) ? call.arguments[0] : undefined,member);
    }
    const flowName = flow.name?.text;
    this.walk(this.tree,node => {
      if (!this.ts.isCallExpression(node) || this.dexName(node.expression) !== "declareRPCMethod") return;
      const [flowClass,methodNameExpression,options] = node.arguments;
      if (!flowName || this.unwrap(flowClass)?.getText(this.tree) !== flowName) return;
      const methodName = this.staticLiteral(methodNameExpression);
      if (typeof methodName !== "string") { this.error("dynamic_rpc_declaration","declareRPCMethod must name the RPC method with a string literal",node); return; }
      const method = flow.members.find(member => this.ts.isMethodDeclaration(member) && member.name && this.propertyName(member.name) === methodName);
      const statement = this.ts.isExpressionStatement(node.parent) ? node.parent : node;
      this.analyzeRPC(method,methodName,options,statement);
    });
  }

  analyzeRPC(method,methodName,optionsExpression,anchor) {
    const nodeID = `rpc:${methodName}`;
    if (this.graph.nodes.some(node => node.id === nodeID)) return;
    this.graph.nodes.push({id:nodeID,kind:"rpc",name:methodName,span:this.span(method || anchor)});
    const options =optionsExpression === undefined ? undefined : this.resolvedValue(optionsExpression,{}).node;
    const optionsObject = options && this.ts.isObjectLiteralExpression(options) ? options : undefined;
    const property = name => optionsObject?.properties.find(candidate => (this.ts.isPropertyAssignment(candidate) || this.ts.isShorthandPropertyAssignment(candidate)) && candidate.name && this.propertyName(candidate.name) === name);
    const valueOf = candidate => this.ts.isShorthandPropertyAssignment(candidate) ? candidate.name : candidate.initializer;
    const nameProperty = property("name");
    const staticName = nameProperty ? this.staticLiteral(valueOf(nameProperty),{}) : undefined;
    const rpcName = typeof staticName === "string" && staticName ? staticName : methodName;
    const actionProperty = property("action");
    const hasStaticName = optionsExpression === undefined ||
      (optionsObject && (!nameProperty || typeof staticName === "string") && !optionsObject.properties.some(candidate => this.ts.isSpreadAssignment(candidate)));
    const isView = hasStaticName && (rpcName === "GetDexSummary" || rpcName === "GetDexDisplay");
    if (method?.body) {
      this.analyzeDecisions(nodeID,method,"rpc");
      this.analyzeResourceAccess(nodeID,method,"rpc",this.flowClass,isView ? rpcName : undefined);
    }
    const lockProblems = this.analyzeRPCLocks(nodeID,rpcName,property("lockAttributes"),valueOf);
    if (isView || actionProperty) {
      for (const problem of lockProblems) this.rpcLockErrors.push({rpcNodeId:nodeID,rpcName,message:problem.message,span:problem.span});
    }
    if (isView) this.viewRPCs.push(this.viewRPCFact(rpcName,method,anchor,!!property("inputCodec")));
    if (actionProperty) this.actions.push(this.actionEntry(method,methodName,nodeID,rpcName,valueOf(actionProperty),anchor));
  }

  viewRPCFact(protocolName,method,anchor,hasInputCodec) {
    const methodDirectives = method ? this.directiveLines(method,"field") : [];
    const directives = anchor !== method && methodDirectives.length === 0 ? this.directiveLines(anchor,"field") : methodDirectives;
    const parameters = method?.parameters.filter(parameter => parameter.name.getText(this.tree) !== "this");
    if (hasInputCodec || parameters?.length > 1) {
      this.error("v2_rpc_signature",`RPC ${protocolName} input must be dex.None`,method || anchor);
    }
    this.validateReadOnlyViewRPC(protocolName,method,new Set());
    const outputKeys = this.viewReturnKeys(method,new Set(),true);
    return {protocolName,directives,outputKeys,span:this.span(method || anchor)};
  }

  validateReadOnlyViewRPC(protocolName,method,visited) {
    if (!method?.body || visited.has(method)) return;
    visited.add(method);
    this.walk(method.body,node => {
      if (this.ts.isCallExpression(node)) {
        const callee = this.unwrap(node.expression);
        const helper = this.resourceHelper(callee,this.flowClass);
        this.validateReadOnlyViewRPC(protocolName,helper?.declaration,visited);
        if (["goTo","goToMany"].includes(this.dexName(callee)) || this.memberCall(callee)?.method === "deleteChannelMessage") {
          this.error("v2_view_rpc_side_effect",`${protocolName} must be read-only`,node);
        }
      }
      if (this.ts.isObjectLiteralExpression(node) && node.properties.some(property => property.name && this.propertyName(property.name) === "output")) {
        for (const property of node.properties) {
          if (property.name && ["nextSteps","cancelingSteps"].includes(this.propertyName(property.name))) {
            this.error("v2_view_rpc_side_effect",`${protocolName} must be read-only`,property);
          }
        }
      }
    });
  }

  viewReturnKeys(method,visited,wrapped) {
    if (!method?.body || visited.has(method) || visited.size >= maximumHelperDepth) return undefined;
    const methodVisits = new Set([...visited,method]);
    const outputs = this.viewReturnExpressions(method.body).map(expression => this.viewExpressionKeys(expression,methodVisits,wrapped));
    const first = outputs[0];
    if (first && outputs.every(keys => keys && keys.length === first.length && keys.every((key,index) => key === first[index]))) return first;
    return undefined;
  }

  viewReturnExpressions(body) {
    if (!this.ts.isBlock(body)) return [body];
    const pending = [body];
    const returns = [];
    while (pending.length) {
      const node = pending.pop();
      if (this.ts.isReturnStatement(node)) {
        returns.push(node.expression);
      } else if (!this.ts.isFunctionLike(node) && !this.ts.isClassLike(node)) {
        for (const child of node.getChildren(this.tree)) pending.push(child);
      }
    }
    return returns;
  }

  viewExpressionKeys(expression,visited,wrapped) {
    const result = this.resolvedValue(expression,{}).node;
    if (!result) return undefined;
    if (this.ts.isConditionalExpression(result)) {
      const left = this.viewExpressionKeys(result.whenTrue,visited,wrapped);
      const right = this.viewExpressionKeys(result.whenFalse,visited,wrapped);
      return left && right && left.length === right.length && left.every((key,index) => key === right[index]) ? left : undefined;
    }
    if (this.ts.isCallExpression(result)) {
      const callee = this.unwrap(result.expression);
      const helper = this.resourceHelper(callee,this.flowClass);
      if (helper) return this.viewReturnKeys(helper.declaration,visited,wrapped);
      const localFunction = this.resolvedValue(callee,{}).node;
      if (localFunction && (this.ts.isArrowFunction(localFunction) || this.ts.isFunctionExpression(localFunction))) {
        return this.viewReturnKeys(localFunction,visited,wrapped);
      }
      return undefined;
    }
    if (!this.ts.isObjectLiteralExpression(result)) return undefined;
    if (result.properties.some(property => !this.propertyValueExpression(property) || this.ts.isComputedPropertyName(property.name))) return undefined;
    const properties = new Map(result.properties.map(property => [this.propertyName(property.name),this.propertyValueExpression(property)]));
    if (wrapped) return this.viewExpressionKeys(properties.get("output"),visited,false);
    return [...properties.keys()].sort();
  }

  startInputFact(stepType,declaration,registration) {
    const method = this.method(declaration,"execute");
    const annotation = method?.parameters.filter(parameter => parameter.name.getText(this.tree) !== "this")[1]?.type;
    const span = this.span(annotation || method || registration);
    try {
      return {stepType,span,input:this.startInputSchema(annotation)};
    } catch (error) {
      if (!(error instanceof StartInputExtractionError)) throw error;
      return {stepType,span,error:error.message};
    }
  }

  startInputSchema(type,ancestors=new Set(),depth=0) {
    if (depth > 32) throw new StartInputExtractionError("nesting exceeds 32 levels");
    if (!type) throw new StartInputExtractionError("execute must declare an input type in this file");
    const ts = this.ts;
    const scalar = {
      [ts.SyntaxKind.StringKeyword]:"string", [ts.SyntaxKind.NumberKeyword]:"number",
      [ts.SyntaxKind.BooleanKeyword]:"boolean", [ts.SyntaxKind.VoidKeyword]:"null",
      [ts.SyntaxKind.UndefinedKeyword]:"null",
    }[type.kind];
    if (scalar) return {kind:scalar};
    if (type.kind === ts.SyntaxKind.BigIntKeyword) return {kind:"integer",minimum:"-9223372036854775808",maximum:"9223372036854775807"};
    if (ts.isParenthesizedTypeNode(type) || (ts.isTypeOperatorNode(type) && type.operator === ts.SyntaxKind.ReadonlyKeyword)) {
      return this.startInputSchema(type.type,ancestors,depth+1);
    }
    if (ts.isLiteralTypeNode(type)) {
      if (type.literal.kind === ts.SyntaxKind.NullKeyword) return {kind:"null"};
      if (ts.isStringLiteral(type.literal)) return {kind:"string",enumValues:[{name:type.literal.text || '""',value:type.literal.text}]};
    }
    if (ts.isUnionTypeNode(type)) {
      const members = type.types.map(member => this.startInputSchema(member,ancestors,depth+1));
      const values = members.filter(member => member.kind !== "null");
      const nullable = values.length < members.length;
      if (values.length === 1) return {...values[0],nullable:nullable || values[0].nullable};
      if (values.length === 0) return {kind:"null"};
      if (values.every(member => member.kind === "string" && member.enumValues)) {
        const enumValues = new Map(values.flatMap(member => member.enumValues).map(entry => [entry.value,entry]));
        return {kind:"string",nullable,enumValues:[...enumValues.values()]};
      }
    }
    if (ts.isArrayTypeNode(type)) return {kind:"array",items:this.startInputSchema(type.elementType,ancestors,depth+1)};
    if (ts.isTypeLiteralNode(type)) return this.startInputObject(type.members,ancestors,depth);
    if (ts.isTypeReferenceNode(type)) return this.startInputReference(type,ancestors,depth);
    throw new StartInputExtractionError(`type ${type.getText(this.tree)} is unsupported`);
  }

  startInputReference(reference,ancestors,depth) {
    const name = reference.typeName.getText(this.tree);
    const argumentsList = reference.typeArguments || [];
    const declaration = this.inputTypes.get(name);
    const binding = this.scopeBindings.get(this.tree)?.has(name) ? this.staticBinding(this.tree,name) : undefined;
    if (binding && this.ts.isEnumDeclaration(binding)) {
      if (binding.members.length && binding.members.every(member => this.ts.isIdentifier(member.name) && member.initializer && this.ts.isStringLiteral(member.initializer))) {
        return {kind:"string",enumValues:binding.members.map(member => ({name:member.name.text,value:member.initializer.text}))};
      }
      throw new StartInputExtractionError(`type ${name} is unsupported`);
    }
    if (declaration && !argumentsList.length && !declaration.typeParameters?.length) {
      if (ancestors.has(declaration)) throw new StartInputExtractionError(`type ${name} is recursive`);
      const nextAncestors = new Set([...ancestors,declaration]);
      if (this.ts.isTypeAliasDeclaration(declaration)) return this.startInputSchema(declaration.type,nextAncestors,depth+1);
      if (!declaration.heritageClauses?.length) return this.startInputObject(declaration.members,nextAncestors,depth);
    }
    if (!declaration && name === "Date" && !argumentsList.length) return {kind:"string",format:"date-time"};
    if (!declaration && ["Array","ReadonlyArray"].includes(name) && argumentsList.length === 1) {
      return {kind:"array",items:this.startInputSchema(argumentsList[0],ancestors,depth+1)};
    }
    if (!declaration && name === "Record" && argumentsList.length === 2) {
      const key = this.startInputSchema(argumentsList[0],ancestors,depth+1);
      if (key.kind !== "string" || key.enumValues || key.format || key.nullable) throw new StartInputExtractionError("map keys must be strings");
      return {kind:"map",values:this.startInputSchema(argumentsList[1],ancestors,depth+1)};
    }
    throw new StartInputExtractionError(`type ${reference.getText(this.tree)} cannot be resolved in this file`);
  }

  startInputObject(members,ancestors,depth) {
    const fields = [];
    for (const member of members) {
      if (!this.ts.isPropertySignature(member) || this.ts.isComputedPropertyName(member.name)) {
        throw new StartInputExtractionError(`object member ${member.getText(this.tree)} must be a named property`);
      }
      const name = this.propertyName(member.name);
      try {
        fields.push({name,required:!member.questionToken,schema:this.startInputSchema(member.type,ancestors,depth+1)});
      } catch (error) {
        if (!(error instanceof StartInputExtractionError)) throw error;
        throw new StartInputExtractionError(`field ${name}: ${error.message}`);
      }
    }
    return {kind:"object",fields};
  }

  // stepLockEdges returns the resource_lock edges of the Step's registration locks.
  stepLockEdges(nodeID,declaration) {
    const edges = [];
    const method = this.method(declaration,"getStepOptions");
    const phases = new Map([["waitForLockAttributes","wait_for"],["executeLockAttributes","execute"]]);
    this.walkStatements(this.handlerStatements(method),"",expression => {
      const options = this.resolvedValue(expression,this.currentFrame).node;
      if (!options || !this.ts.isObjectLiteralExpression(options)) return false;
      for (const property of options.properties) {
        const phase = property.name && phases.get(this.propertyName(property.name));
        if (!phase) continue;
        const list = this.resolvedValue(this.propertyValueExpression(property),this.currentFrame).node;
        if (!list || !this.ts.isArrayLiteralExpression(list)) continue;
        for (const element of list.elements) {
          const lock = this.resolvedValue(element,this.currentFrame).node;
          const resourceID = this.lockedAttribute(lock);
          if (resourceID) edges.push({kind:"resource_lock",from:nodeID,to:resourceID,label:"lock",span:this.span(lock),metadata:{phase}});
        }
      }
      return true;
    });
    return edges;
  }

  lockedAttribute(expression) {
    if (!expression || !this.ts.isCallExpression(expression) || !this.ts.isPropertyAccessExpression(expression.expression) || expression.expression.name.text !== "lock") return undefined;
    const resourceID = this.resourceFor(expression.expression.expression);
    return resourceID?.startsWith("resource:attribute:") ? resourceID : undefined;
  }

  analyzeRPCLocks(nodeID,rpcName,lockProperty,valueOf) {
    const problems = [];
    if (!lockProperty) return problems;
    const expression = valueOf(lockProperty);
    const list = this.resolvedValue(expression,{}).node;
    if (!list || !this.ts.isArrayLiteralExpression(list)) {
      problems.push({message:`lockAttributes of RPC ${rpcName} is not an array literal (${this.lineText(expression)}); list Attribute lock() calls directly`,span:this.span(expression)});
      return problems;
    }
    const recorded = new Set();
    for (const element of list.elements) {
      const lock = this.resolvedValue(element,{}).node;
      const resourceID = this.lockedAttribute(lock);
      if (!resourceID) {
        problems.push({message:`lock ${this.lineText(element)} of RPC ${rpcName} is not a lock() call on an Attribute declared in this file`,span:this.span(element)});
        continue;
      }
      if (recorded.has(resourceID)) continue;
      recorded.add(resourceID);
      this.graph.edges.push({kind:"resource_lock",from:nodeID,to:resourceID,label:"lock",span:this.span(lock),metadata:{phase:"rpc",rpcName}});
    }
    return problems;
  }

  actionEntry(method,methodName,nodeID,rpcName,expression,anchor) {
    const entry = {rpcNodeId:nodeID,rpcName,span:this.span(expression),error:"",label:"",requiredPermission:"",condition:null,directives:this.directives(anchor),input:this.actionInput(method,methodName)};
    const fail = message => ({...entry,error:message});
    const action = this.resolvedValue(expression,{}).node;
    if (!action || !this.ts.isNewExpression(action) || this.dexName(action.expression) !== "Action" || action.arguments?.length !== 1) {
      return fail(`RPC ${rpcName} Action (${this.lineText(expression)}) must directly construct new Action({ label, requiredPermission, condition }) from @superdurable/dex`);
    }
    const fields = this.resolvedValue(action.arguments[0],{}).node;
    if (!fields || !this.ts.isObjectLiteralExpression(fields)) return fail(`RPC ${rpcName} Action (${this.lineText(action.arguments[0])}) must pass an object literal to new Action`);
    const values = new Map();
    for (const candidate of fields.properties) {
      if (!(this.ts.isPropertyAssignment(candidate) || this.ts.isShorthandPropertyAssignment(candidate)) || !candidate.name) {
        return fail(`RPC ${rpcName} Action (${this.lineText(candidate)}) must list label, requiredPermission, and condition as plain properties`);
      }
      values.set(this.propertyName(candidate.name),this.ts.isShorthandPropertyAssignment(candidate) ? candidate.name : candidate.initializer);
    }
    for (const name of values.keys()) if (!["label","requiredPermission","condition"].includes(name)) return fail(`RPC ${rpcName} Action has unknown property ${name}`);
    const label = this.staticLiteral(values.get("label"),{});
    if (typeof label !== "string") return fail(`RPC ${rpcName} Action label (${this.lineText(values.get("label") || action)}) must be a string literal or a module-level string constant`);
    const permission = this.staticLiteral(values.get("requiredPermission"),{});
    if (typeof permission !== "string") return fail(`RPC ${rpcName} Action requiredPermission (${this.lineText(values.get("requiredPermission") || action)}) must be a string literal or a module-level string constant`);
    entry.label = label;
    entry.requiredPermission = permission;
    const conditionExpression = values.get("condition");
    const condition = conditionExpression === undefined ? undefined : this.resolvedValue(conditionExpression,{}).node;
    if (!condition || condition.kind === this.ts.SyntaxKind.UndefinedKeyword || (this.ts.isIdentifier(condition) && condition.text === "undefined")) return entry;
    if (!this.ts.isCallExpression(condition) || this.dexName(condition.expression) !== "whenAttributeMatches" || condition.arguments.length < 2) {
      return fail(`RPC ${rpcName} Action condition (${this.lineText(conditionExpression)}) must directly call whenAttributeMatches(attribute, AttributeMatch.equalTo(value), ...)`);
    }
    const resourceID = this.resourceFor(condition.arguments[0]);
    const attribute = resourceID && this.graph.nodes.find(node => node.id === resourceID && node.kind === "attribute");
    if (!attribute) return fail(`RPC ${rpcName} Action condition Attribute (${this.lineText(condition.arguments[0])}) must reference an Attribute declared in this file`);
    const matches = [];
    for (const match of condition.arguments.slice(1)) {
      const call = this.resolvedValue(match,{}).node;
      const isEqualTo = call && this.ts.isCallExpression(call) && this.ts.isPropertyAccessExpression(call.expression) && call.expression.name.text === "equalTo" && this.dexName(call.expression.expression) === "AttributeMatch" && call.arguments.length === 1;
      if (!isEqualTo) return fail(`RPC ${rpcName} Action condition match (${this.lineText(match)}) must directly call AttributeMatch.equalTo with a literal`);
      const value = this.actionScalar(call.arguments[0]);
      if (value === undefined) return fail(`RPC ${rpcName} Action condition value (${this.lineText(call.arguments[0])}) must be a string, number, bigint, or boolean literal or a module-level constant`);
      matches.push(value);
    }
    entry.condition = {attributeKey:attribute.name,values:matches,span:this.span(condition)};
    return entry;
  }

  actionScalar(expression) {
    let node = this.resolvedValue(expression,{}).node;
    let sign = 1;
    if (node && this.ts.isPrefixUnaryExpression(node) && node.operator === this.ts.SyntaxKind.MinusToken) { sign = -1; node = this.resolvedValue(node.operand,{}).node; }
    if (node && this.ts.isBigIntLiteral(node)) {
      const value = BigInt(node.text.slice(0,-1)) * BigInt(sign);
      return value >= BigInt(Number.MIN_SAFE_INTEGER) && value <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(value) : undefined;
    }
    const value = this.staticLiteral(node,{});
    if (typeof value === "number") return Number.isFinite(value) ? sign * value : undefined;
    return sign === 1 && (typeof value === "string" || typeof value === "boolean") ? value : undefined;
  }

  actionInput(method,methodName) {
    if (!method) return {kind:"none",typeName:"",error:`RPC method ${methodName} must be declared in the Flow class`,fields:[]};
    const parameter = method.parameters[1];
    if (!parameter) return {kind:"none",typeName:"",error:"",fields:[]};
    const typeNode = parameter.type;
    const typeName = typeNode && this.ts.isTypeReferenceNode(typeNode) && this.ts.isIdentifier(typeNode.typeName) ? typeNode.typeName.text : "";
    const declaration = typeName ? this.inputTypes.get(typeName) : undefined;
    const members = declaration && (this.ts.isInterfaceDeclaration(declaration) ? declaration.members : this.ts.isTypeLiteralNode(declaration.type) ? declaration.type.members : undefined);
    if (!members) return {kind:"object",typeName:typeName || (typeNode ? typeNode.getText(this.tree) : ""),error:`Action RPC ${methodName} input must be annotated with an interface or object type alias declared in this file`,fields:[]};
    const fields = [];
    for (const member of members) {
      if (!this.ts.isPropertySignature(member) || !member.name || !member.type) return {kind:"object",typeName,error:`Action RPC ${methodName} input type ${typeName} must contain only typed properties`,fields:[]};
      let type = member.type;
      let required = !member.questionToken;
      if (this.ts.isUnionTypeNode(type)) {
        const kept = type.types.filter(part => !(part.kind === this.ts.SyntaxKind.UndefinedKeyword || (this.ts.isLiteralTypeNode(part) && part.literal.kind === this.ts.SyntaxKind.NullKeyword)));
        if (kept.length !== type.types.length) required = false;
        if (kept.length === 1) type = kept[0];
      }
      fields.push({fieldName:this.propertyName(member.name),valueType:this.canonicalValueType(type.getText(this.tree)),required});
    }
    return {kind:"object",typeName,error:"",fields};
  }

  canonicalValueType(text) {
    return {string:"string",number:"double",bigint:"int64",boolean:"bool",Date:"datetime"}[text] || text;
  }

  lineText(node) {
    const span = this.span(node);
    const text = node?.getText ? node.getText(this.tree).replace(/\s+/g," ") : "";
    const shortText = text.length > 60 ? `${text.slice(0,57)}...` : text;
    return span ? `${shortText} at line ${span.startLine}` : shortText;
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

  // Phase is execute, timeout, or rpc; only an RPC may return no decision.
  analyzeDecisions(ownerID,handler,phase="execute") {
    const statements = this.handlerStatements(handler);
    const locals = this.localMovements(handler.body);
    const outcomes = [];
    const hiddenDecisions = [];
    this.walkStatements(statements,"",(expression,condition) => {
      outcomes.push(...this.decisionOutcomes(expression,condition,locals,hiddenDecisions,phase));
    });
    if (!outcomes.length && !hiddenDecisions.length && phase !== "rpc") hiddenDecisions.push(handler);
    for (const hidden of hiddenDecisions) this.addHiddenDecision(ownerID,hidden,phase);
    if (!outcomes.length) return;
    if (outcomes.length > 1) {
      for (const outcome of outcomes) if (!outcome.condition) outcome.condition = "otherwise";
      this.graph.nodes.push({id:`decision-dispatch:${ownerID}`,kind:"decision_dispatch",name:"Decision",parentId:ownerID,phase,span:this.span(handler)});
    }
    const decisionIDs = new Set();
    for (const outcome of outcomes) {
      // A result reached through a helper call also names each call site, so every call keeps its own decision.
      const decisionID = `decision:${ownerID}:${[...(outcome.callSites || []),`${outcome.span.startLine}:${outcome.span.startColumn}`].join(":")}`;
      if (decisionIDs.has(decisionID)) continue;
      decisionIDs.add(decisionID);
      const details = {type:outcome.decisionType};
      if (outcome.checkedChannels.length) details.checkedChannels = outcome.checkedChannels;
      const cancellations = outcome.transitions.filter(transition => transition.kind === "cancel").map(transition => ({stepId:this.resolveTarget(transition.target,transition.span,transition.targetNode),scope:transition.label === "withCancelingSiblingSteps" ? "siblings" : "all"}));
      if (cancellations.length) details.cancellations = cancellations;
      this.graph.nodes.push({id:decisionID,kind:"decision",name:outcome.decisionType,parentId:ownerID,condition:outcome.condition,phase,span:outcome.span,decision:details});
      for (const transition of outcome.transitions) {
        if (transition.kind === "terminal") continue;
        const edge = {kind:transition.kind,from:decisionID,to:this.resolveTarget(transition.target,transition.span,transition.targetNode),label:transition.label,span:transition.span};
        if (transition.multiplicity) edge.multiplicity = transition.multiplicity;
        this.graph.edges.push(edge);
      }
    }
  }

  addHiddenDecision(ownerID,node,phase) {
    const span = this.span(node);
    const nodeID = `unknown:${ownerID}:${span.startLine}:${span.startColumn}`;
    if (this.addNodeOnce({id:nodeID,kind:"unknown",name:"Dynamic Dex decision",span})) this.graph.edges.push({kind:"transition",from:ownerID,to:nodeID,label:"dynamic",span});
    const message = {
      rpc:`RPC ${ownerID.slice("rpc:".length)} must directly return an RPCResult object literal, such as { output, nextSteps }, or return nothing (${this.lineText(node)})`,
      timeout:`handleTimeout must directly return a Dex decision such as goTo or forceFail (${this.lineText(node)})`,
    }[phase] || "execute must directly return a Dex decision such as goTo or gracefulComplete";
    this.error("hidden_dex_decision",message,node);
  }

  // Every return ends its path; statements after one that always exits are unreachable and skipped.
  // Returns whether the statements contain a return.
  walkStatements(statements,condition,visit) {
    let hasReturn = false;
    for (const statement of statements) {
      if (this.ts.isReturnStatement(statement)) {
        visit(statement.expression,condition);
        hasReturn = true;
      } else if (this.ts.isBlock(statement)) {
        hasReturn = this.walkStatements(statement.statements,condition,visit) || hasReturn;
      } else if (this.ts.isIfStatement(statement)) {
        const branch = statement.expression.getText(this.tree);
        const thenStatements = this.blockStatements(statement.thenStatement);
        const thenHasReturn = this.walkStatements(thenStatements,this.combine(condition,branch),visit);
        hasReturn = thenHasReturn || hasReturn;
        if (statement.elseStatement) hasReturn = this.walkStatements(this.blockStatements(statement.elseStatement),this.combine(condition,this.negate(branch)),visit) || hasReturn;
        else if (thenHasReturn && this.alwaysExits(thenStatements)) condition = this.combine(condition,this.negate(branch));
      } else if (this.ts.isSwitchStatement(statement)) {
        const subject = statement.expression.getText(this.tree);
        for (const clause of statement.caseBlock.clauses) {
          const label = this.ts.isCaseClause(clause) ? `${subject} === ${clause.expression.getText(this.tree)}` : "default";
          hasReturn = this.walkStatements(clause.statements,this.combine(condition,label),visit) || hasReturn;
        }
      } else if (this.ts.isIterationStatement(statement,false) || this.ts.isLabeledStatement(statement)) {
        hasReturn = this.walkStatements(this.blockStatements(statement.statement),condition,visit) || hasReturn;
      } else if (this.ts.isTryStatement(statement)) {
        hasReturn = this.walkStatements(statement.tryBlock.statements,condition,visit) || hasReturn;
        if (statement.catchClause) hasReturn = this.walkStatements(statement.catchClause.block.statements,this.combine(condition,"exception"),visit) || hasReturn;
        if (statement.finallyBlock) hasReturn = this.walkStatements(statement.finallyBlock.statements,condition,visit) || hasReturn;
      }
      if (this.alwaysExits([statement])) break;
    }
    return hasReturn;
  }

  decisionOutcomes(expression,condition,locals,hiddenDecisions,phase,visitedHelpers=new Set()) {
    expression = this.unwrap(expression);
    if (!expression) return [];
    if (this.ts.isConditionalExpression(expression)) {
      const branch = expression.condition.getText(this.tree);
      return [
        ...this.decisionOutcomes(expression.whenTrue,this.combine(condition,branch),locals,hiddenDecisions,phase,visitedHelpers),
        ...this.decisionOutcomes(expression.whenFalse,this.combine(condition,this.negate(branch)),locals,hiddenDecisions,phase,visitedHelpers),
      ];
    }
    if (phase === "rpc") return this.rpcResultOutcomes(expression,condition,hiddenDecisions,visitedHelpers);
    const decisionType = this.decisionType(expression);
    if (!decisionType) { hiddenDecisions.push(expression); return []; }
    return [{decisionType,condition,transitions:this.parseDecision(expression,locals),checkedChannels:this.checkedChannels(expression),span:this.span(expression)}];
  }

  // An RPC returns nothing or an RPCResult; a returned Flow method call contributes that method's results.
  // Supported: an object literal returned directly, nothing, or a call to a non-recursive Flow method or file function.
  rpcResultOutcomes(expression,condition,hiddenDecisions,visitedHelpers) {
    if (this.isUndefinedValue(expression)) return [];
    const result = this.unwrap(expression);
    const helper = this.ts.isCallExpression(result) ? this.resourceHelper(this.unwrap(result.expression),this.flowClass) : undefined;
    if (helper) {
      if (visitedHelpers.has(helper.declaration) || visitedHelpers.size >= maximumHelperDepth) { hiddenDecisions.push(expression); return []; }
      const helperVisits = new Set([...visitedHelpers,helper.declaration]);
      const outcomes = [];
      this.walkStatements(this.handlerStatements(helper.declaration),condition,(returned,returnCondition) => {
        outcomes.push(...this.decisionOutcomes(returned,returnCondition,new Map(),hiddenDecisions,"rpc",helperVisits));
      });
      const callSpan = this.span(expression);
      return outcomes.map(outcome => ({...outcome,callSites:[`${callSpan.startLine}:${callSpan.startColumn}`,...(outcome.callSites || [])]}));
    }
    if (!this.ts.isObjectLiteralExpression(result)) { hiddenDecisions.push(expression); return []; }
    const transitions = [];
    for (const property of result.properties) {
      if (!(this.ts.isPropertyAssignment(property) || this.ts.isShorthandPropertyAssignment(property)) || this.ts.isComputedPropertyName(property.name)) {
        this.dynamicTarget("RPCResult must list output, nextSteps, and cancelingSteps as plain properties",property);
        continue;
      }
      const value = this.ts.isShorthandPropertyAssignment(property) ? property.name : property.initializer;
      const name = this.propertyName(property.name);
      if (name === "nextSteps") {
        for (const element of this.rpcListElements(value,"nextSteps")) {
          const movement = this.parseMovement(element);
          if (movement) transitions.push(movement);
          else this.dynamicTarget("each RPCResult nextSteps element must be a StepMovement.of call written in the list",element);
        }
      } else if (name === "cancelingSteps") {
        for (const element of this.rpcListElements(value,"cancelingSteps")) {
          if (this.ts.isSpreadElement(element)) { this.dynamicTarget("RPCResult cancelingSteps must list each Step target directly",element); continue; }
          transitions.push({kind:"cancel",target:this.stepTarget(element),targetNode:element,label:"cancelingSteps",span:this.span(element)});
        }
      }
    }
    return [{decisionType:"rpcResult",condition,transitions,checkedChannels:[],span:this.span(expression)}];
  }

  // Supported: an array literal in the result, or a handler const array literal that nothing else references.
  rpcListElements(expression,property) {
    if (this.isUndefinedValue(expression)) return [];
    let list = this.unwrap(expression);
    if (this.ts.isIdentifier(list)) list = this.singleUseConstInitializer(list);
    if (list && this.ts.isArrayLiteralExpression(list)) return list.elements;
    this.dynamicTarget(`RPCResult ${property} must be an array literal written in the returned object, or a const array literal used only there`,expression);
    return [];
  }

  // Returns the initializer of a block-level const when this identifier is its only reference.
  singleUseConstInitializer(identifier) {
    const declaration = this.localBindingDeclaration(identifier);
    const statement = declaration?.parent?.parent;
    if (!declaration || !this.ts.isVariableDeclaration(declaration) || !declaration.initializer || !statement || !this.ts.isVariableStatement(statement)) return undefined;
    if (!(statement.declarationList.flags & this.ts.NodeFlags.Const) || !this.ts.isBlock(statement.parent)) return undefined;
    return this.valueReferences(statement.parent,declaration).length === 1 ? this.unwrap(declaration.initializer) : undefined;
  }

  // Only a missing value, void, or the global undefined with no binding of that name in the file is empty.
  isUndefinedValue(expression) {
    const value = this.unwrap(expression);
    return !value || this.ts.isVoidExpression(value) || (this.ts.isIdentifier(value) && value.text === "undefined" && !this.bindingScope(value));
  }

  decisionType(expression) {
    if (!this.ts.isCallExpression(expression)) return undefined;
    if (this.isJoinMethodCall(expression,["endBranch"])) return "deadEnd";
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
      if (this.ts.isIdentifier(spread)) return (locals.get(spread.text) || []).map(movement => movement.hasExactMultiplicity ? movement : {...movement,multiplicity:movement.multiplicity || "×N"});
      if (this.isJoinMethodCall(spread,["fork","forkWithRoundKey"])) return this.joinForkMovements(spread);
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

  isJoinMethodCall(expression,methodNames) {
    if (!this.ts.isCallExpression(expression) || !this.ts.isPropertyAccessExpression(expression.expression)) return false;
    if (!methodNames.includes(expression.expression.name.text)) return false;
    return this.joinResourceIDs.has(this.resourceFor(expression.expression.expression));
  }

  // Join.fork and forkWithRoundKey take branch builders second and the join builder third.
  joinForkMovements(call) {
    const [, branchBuilders, joinBuilder] = call.arguments;
    const movements = branchBuilders ? this.branchBuilderListMovements(call,branchBuilders) : [];
    if (joinBuilder) movements.push(...this.builderMovements(joinBuilder));
    return movements.map(movement => ({...movement,hasExactMultiplicity:true}));
  }

  branchBuilderListMovements(call,branchBuilders) {
    const builders = this.unwrap(branchBuilders);
    const dynamicMessage = "Join branch builders must build their movements directly in the handler";
    if (builders && this.ts.isIdentifier(builders)) {
      const listed = this.localListMovements(call,builders,builder => this.builderMovements(builder));
      if (!listed?.movements) this.dynamicTarget(dynamicMessage,branchBuilders);
      return listed?.movements || [];
    }
    const movements = [];
    if (builders && this.ts.isArrayLiteralExpression(builders)) {
      for (const builder of builders.elements) movements.push(...this.builderMovements(builder));
      return movements;
    }
    if (builders && this.ts.isCallExpression(builders)) movements.push(...this.callbackMovements(builders));
    if (!movements.length) this.dynamicTarget(dynamicMessage,branchBuilders);
    return movements;
  }

  // Reads a local list's movements as they stand at `use`.
  // Returns undefined when the list is not a local array, or {movements: undefined} when its contents are undetermined.
  localListMovements(use,listIdentifier,movementsOfElement) {
    const declaration = this.localBindingDeclaration(listIdentifier);
    if (!declaration || !this.ts.isVariableDeclaration(declaration) || !declaration.initializer) return undefined;
    const initializer = this.unwrap(declaration.initializer);
    let movements;
    if (this.ts.isArrayLiteralExpression(initializer)) {
      movements = initializer.elements.flatMap(element => movementsOfElement(element));
    } else if (this.ts.isCallExpression(initializer) && !this.isJoinMethodCall(initializer,["fork","forkWithRoundKey"])) {
      movements = this.callbackMovements(initializer);
      if (!movements.length) return {movements:undefined};
    } else {
      return undefined;
    }
    let isDetermined = true;
    this.walk(this.declaringScope(declaration),node => {
      if (!isDetermined || !this.ts.isIdentifier(node) || node === listIdentifier || node === declaration.name || node.text !== listIdentifier.text) return;
      if (this.localBindingDeclaration(node) !== declaration || this.isListRead(node) || node.pos >= use.pos) return;
      const push = this.pushOnList(node);
      const placement = this.placementBefore(push || node,use);
      if (placement === "unreachable") return;
      if (!push || placement === "unknown") { isDetermined = false; return; }
      for (const argument of push.arguments) {
        const pushed = movementsOfElement(argument);
        movements.push(...(placement === "repeated" ? pushed.map(movement => ({...movement,multiplicity:"×N"})) : pushed));
      }
    });
    return {movements:isDetermined ? movements : undefined};
  }

  pushOnList(identifier) {
    const access = identifier.parent;
    if (!access || !this.ts.isPropertyAccessExpression(access) || access.expression !== identifier || access.name.text !== "push") return undefined;
    return this.ts.isCallExpression(access.parent) && access.parent.expression === access ? access.parent : undefined;
  }

  // Reads leave a list unchanged: its length, a spread into goToMany, or a Join fork argument.
  isListRead(identifier) {
    const parent = identifier.parent;
    if (this.ts.isPropertyAccessExpression(parent) && parent.expression === identifier && parent.name.text === "length") return !this.isWriteTarget(parent);
    if (this.ts.isSpreadElement(parent) && this.ts.isCallExpression(parent.parent) && this.dexName(parent.parent.expression) === "goToMany") return true;
    return this.isJoinMethodCall(parent,["fork","forkWithRoundKey"]) && parent.arguments[1] === identifier;
  }

  // Classifies a list change relative to its use: "once", "repeated", "unreachable", or "unknown".
  placementBefore(change,use) {
    const useAncestors = new Set();
    for (let node = use; node; node = node.parent) useAncestors.add(node);
    let placement = "once";
    let child = change;
    for (let node = change.parent; node; child = node, node = node.parent) {
      if (useAncestors.has(node)) {
        const isExclusiveBranch = this.ts.isIfStatement(node) || this.ts.isConditionalExpression(node) || this.ts.isCaseBlock(node);
        if (isExclusiveBranch && child !== node.expression && child !== node.condition) return "unreachable";
        return placement;
      }
      if (this.ts.isIterationStatement(node,false) && child === node.statement) placement = "repeated";
      else if (this.isIterationCallback(node)) placement = "repeated";
      else if (this.ts.isFunctionLike(node) || this.ts.isIfStatement(node) || this.ts.isConditionalExpression(node) || this.ts.isCaseClause(node) || this.ts.isDefaultClause(node) || this.ts.isTryStatement(node) || this.ts.isCatchClause(node) || this.ts.isLabeledStatement(node) || (this.ts.isBinaryExpression(node) && [this.ts.SyntaxKind.AmpersandAmpersandToken,this.ts.SyntaxKind.BarBarToken,this.ts.SyntaxKind.QuestionQuestionToken].includes(node.operatorToken.kind))) return "unknown";
    }
    return "unknown";
  }

  // Only synchronous Array iteration callbacks run as loops; other callbacks may never run.
  isIterationCallback(node) {
    if (!this.ts.isArrowFunction(node) && !this.ts.isFunctionExpression(node)) return false;
    const call = node.parent;
    if (!this.ts.isCallExpression(call) || call.arguments[0] !== node || !this.ts.isPropertyAccessExpression(call.expression)) return false;
    if (!["forEach","map","flatMap","filter","reduce","some","every"].includes(call.expression.name.text)) return false;
    return this.isProvableArray(call.expression.expression,new Set());
  }

  // Accepts an array literal, Array.from, an array-returning call on a provable array, or a binding to one.
  isProvableArray(expression,seen) {
    const value = this.unwrap(expression);
    if (!value || seen.has(value)) return false;
    seen.add(value);
    if (this.ts.isArrayLiteralExpression(value)) return true;
    if (this.ts.isCallExpression(value) && this.ts.isPropertyAccessExpression(value.expression)) {
      const receiver = value.expression.expression;
      const method = value.expression.name.text;
      if (method === "from" && this.ts.isIdentifier(receiver) && receiver.text === "Array") return true;
      return ["map","filter","flatMap","slice","concat"].includes(method) && this.isProvableArray(receiver,seen);
    }
    if (!this.ts.isIdentifier(value)) return false;
    const declaration = this.localBindingDeclaration(value);
    if (!declaration) return false;
    if (declaration.type && this.isArrayTypeNode(declaration.type)) return true;
    return this.ts.isVariableDeclaration(declaration) && declaration.initializer !== undefined && this.isProvableArray(declaration.initializer,seen);
  }

  isArrayTypeNode(typeNode) {
    if (this.ts.isArrayTypeNode(typeNode)) return true;
    if (this.ts.isTypeOperatorNode(typeNode) && typeNode.operator === this.ts.SyntaxKind.ReadonlyKeyword) return this.isArrayTypeNode(typeNode.type);
    return this.ts.isTypeReferenceNode(typeNode) && this.ts.isIdentifier(typeNode.typeName) && ["Array","ReadonlyArray"].includes(typeNode.typeName.text);
  }

  // Classifies how often a push runs within its handler: "once", "repeated", or "unknown".
  pushRepetition(push) {
    for (let node = push.parent; node; node = node.parent) {
      if (this.ts.isIterationStatement(node,false) && this.isWithin(push,node.statement)) return "repeated";
      if (this.isIterationCallback(node)) return "repeated";
      if (this.ts.isFunctionLike(node)) return this.ts.isArrowFunction(node) || this.ts.isFunctionExpression(node) ? "unknown" : "once";
    }
    return "once";
  }

  isWithin(node,ancestor) {
    for (let current = node; current; current = current.parent) if (current === ancestor) return true;
    return false;
  }

  declaringScope(declaration) {
    let scope = declaration.parent;
    while (scope && !this.ts.isBlock(scope) && !this.ts.isSourceFile(scope)) scope = scope.parent;
    return scope;
  }

  // Finds the nearest enclosing declaration of an identifier's name within its handler.
  localBindingDeclaration(identifier) {
    const name = identifier.text;
    const matches = candidate => candidate && this.ts.isIdentifier(candidate.name) && candidate.name.text === name;
    for (let scope = identifier.parent; scope; scope = scope.parent) {
      if (this.ts.isBlock(scope) || this.ts.isSourceFile(scope)) {
        for (const statement of scope.statements) {
          if (!this.ts.isVariableStatement(statement)) continue;
          const declaration = statement.declarationList.declarations.find(matches);
          if (declaration) return declaration;
        }
      } else if (this.ts.isFunctionLike(scope)) {
        const parameter = scope.parameters.find(matches);
        if (parameter) return parameter;
        if (!this.ts.isArrowFunction(scope)) return undefined;
      } else if ((this.ts.isForStatement(scope) || this.ts.isForOfStatement(scope) || this.ts.isForInStatement(scope)) && scope.initializer && this.ts.isVariableDeclarationList(scope.initializer)) {
        const declaration = scope.initializer.declarations.find(matches);
        if (declaration) return declaration;
      } else if (this.ts.isCatchClause(scope) && matches(scope.variableDeclaration)) {
        return scope.variableDeclaration;
      }
    }
    return undefined;
  }

  builderMovements(builder) {
    const value = this.unwrap(builder);
    const movements = [];
    if (value && (this.ts.isArrowFunction(value) || this.ts.isFunctionExpression(value))) {
      this.walk(value.body,node => {
        const movement = this.parseMovement(node);
        if (movement) movements.push(movement);
      });
    }
    if (!movements.length) this.dynamicTarget("Join builders must return StepMovement.of with a static target",builder);
    return movements;
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
        else if (this.ts.isArrayLiteralExpression(value)) result.set(node.name.text,value.elements.flatMap(element => this.movementArgument(element,result)).map(movement => ({...movement,hasExactMultiplicity:true})));
        else if (this.ts.isCallExpression(value)) {
          const movements = this.isJoinMethodCall(value,["fork","forkWithRoundKey"]) ? this.joinForkMovements(value) : this.callbackMovements(value);
          if (movements.length) result.set(node.name.text,movements);
        }
      }
      if (this.ts.isCallExpression(node) && this.ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === "push" && this.ts.isIdentifier(node.expression.expression)) {
        for (const argument of node.arguments) {
          const movement = this.parseMovement(argument);
          if (!movement) continue;
          const name = node.expression.expression.text;
          // A push outside loops and callbacks adds one movement, even under a condition.
          const multiplicity = this.pushRepetition(node) === "once" ? undefined : "×N";
          result.set(name,[...(result.get(name) || []),{...movement,multiplicity,hasExactMultiplicity:true}]);
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
    this.addNodeOnce({id:`unknown:step:${span.startLine}:${span.startColumn}`,kind:"unknown",name:"Dynamic Step",span});
    this.error("dynamic_step_target",message,node);
  }

  // A helper reached from several call sites is read again for each one, so its unknown nodes repeat.
  addNodeOnce(node) {
    if (this.graph.nodes.some(existing => existing.id === node.id)) return false;
    this.graph.nodes.push(node);
    return true;
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
    // Only a binding that resolvedValue proves names a resource; there is no fallback by name.
    return this.resourceNodes.get(resolved);
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
    return this.directiveLines(declaration).map(directive => directive.text);
  }

  directiveLines(declaration,name) {
    let line = this.tree.getLineAndCharacterOfPosition(declaration.getStart(this.tree)).line;
    const lines = this.source.split(/\r?\n/);
    const directives = [];
    while (line > 0 && lines[line-1].trimStart().startsWith("//")) {
      const raw = lines[--line];
      const trimmed = raw.trimStart();
      const text = trimmed.slice(2).trim();
      if (text.startsWith("dex:") && (!name || text.split(/\s/,1)[0] === `dex:${name}`)) {
        directives.push({text,span:{startLine:line+1,startColumn:raw.length-trimmed.length+1,endLine:line+1,endColumn:raw.length+1}});
      }
    }
    return directives.reverse();
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
    if (this.ts.isArrayLiteralExpression(parent) && this.isRPCCancelingTargets(parent)) return {};
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
    // declareRPCMethod only reads a method descriptor from the class prototype.
    if (this.ts.isCallExpression(parent) && parent.arguments[0] === use && this.dexName(parent.expression) === "declareRPCMethod") return;
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

  // Dex reads the cancelingSteps of an RPCResult that a Flow method returns as Step targets.
  isRPCCancelingTargets(array) {
    const property = array.parent;
    if (!this.ts.isPropertyAssignment(property) || property.initializer !== array || this.propertyName(property.name) !== "cancelingSteps") return false;
    let result = property.parent;
    while (this.isTransparentWrapper(result.parent) || (this.ts.isConditionalExpression(result.parent) && result.parent.condition !== result)) result = result.parent;
    if (!this.ts.isReturnStatement(result.parent)) return false;
    const method = this.ts.findAncestor(result.parent,node => this.ts.isFunctionLike(node));
    return !!method && this.ts.isMethodDeclaration(method) && method.parent === this.flowClass;
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

  typeName(declaration,methodName,reportProblem=true) {
    const method = this.method(declaration,methodName);
    if (!method) return declaration.name.text;
    const returns = [];
    this.walkStatements(this.handlerStatements(method),"",expression => { returns.push(this.staticLiteral(expression)); return true; });
    if (reportProblem && (!returns.length || returns.some(value => typeof value !== "string" || value !== returns[0]))) this.error("dynamic_type_name",`${methodName} must return one static string`,method);
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
  // Like walk, but skips statements after one that always exits in the same statement list.
  walkReachable(node,visit) {
    if (!node) return;
    visit(node);
    if (!this.ts.isBlock(node) && !this.ts.isCaseClause(node) && !this.ts.isDefaultClause(node)) { this.ts.forEachChild(node,child => this.walkReachable(child,visit)); return; }
    if (this.ts.isCaseClause(node)) this.walkReachable(node.expression,visit);
    for (const statement of node.statements) {
      this.walkReachable(statement,visit);
      if (this.alwaysExits([statement])) break;
    }
  }
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
  result() {
    const attributes = this.graph.nodes.filter(node => node.kind === "attribute")
      .sort((left,right) => left.span.startLine - right.span.startLine || left.span.startColumn - right.span.startColumn)
      .map(node => this.attributeFact(node));
    return {graph:this.graph,annotations:this.annotations,factories:this.uses,attributes,actions:this.actions,rpcLockErrors:this.rpcLockErrors,persistenceResourceIds:this.persistenceResourceIds,diagnosticResourceIds:this.diagnosticResourceIds,viewRPCs:this.viewRPCs,startInput:this.startInput,methodExternalCalls:this.methodExternalCalls,undrawnLockEdges:this.undrawnLockEdges};
  }

  // recordMethodExternalCalls marks each Step waitFor and execute that holds a lock an RPC takes, with a fail-closed allowlist.
  // Other methods get no marker, which means unchecked; they cannot warn.
  recordMethodExternalCalls() {
    const rpcLockedResources = new Set(this.graph.edges.filter(edge => edge.kind === "resource_lock" && edge.metadata?.phase === "rpc").map(edge => edge.to));
    const sharedPhases = new Set();
    for (const edge of [...this.graph.edges,...this.undrawnLockEdges]) {
      const phase = edge.metadata?.phase;
      if (edge.kind === "resource_lock" && (phase === "execute" || phase === "wait_for") && rpcLockedResources.has(edge.to)) sharedPhases.add(`${edge.from} ${phase}`);
    }
    // Building the type checker costs more than the rest of the analysis, so build it only when a phase can warn.
    if (!sharedPhases.size) return;
    const problem = this.createTypeChecker();
    for (const {nodeID,stepName,instance} of this.lockedMethodCandidates) {
      this.currentFrame = {instance};
      // Field reads resolve through the Step instance, so cached helper results hold for one Step only.
      this.exactHelperResults = new Map();
      for (const [phase,methodName] of [["wait_for","waitFor"],["execute","execute"]]) {
        if (!sharedPhases.has(`${nodeID} ${phase}`)) continue;
        const method = this.method(instance.declaration,methodName);
        if (!method) {
          if (phase === "execute") this.methodExternalCalls.push({nodeId:nodeID,phase,calls:[{callee:`execute of ${stepName}, which is not defined in this file`}]});
          continue;
        }
        let calls;
        if (problem) calls = [{callee:`code dexcli could not type-check (${problem})`,span:this.span(method)}];
        else if (!method.body) calls = [{callee:`${methodName}, which has no body in this file`,span:this.span(method)}];
        else {
          this.stepInputParameter = this.hasDataInputCodec(instance.declaration) ? method.parameters[1] : undefined;
          calls = this.bodyExternalCalls([method.body],0,false).calls;
          this.stepInputParameter = undefined;
        }
        this.methodExternalCalls.push({nodeId:nodeID,phase,calls});
      }
      this.currentFrame = {};
    }
  }

  // createTypeChecker type-checks the source with the compiler's default library and the project's own
  // relative imports; package imports, including the Dex SDK, stay unresolved.
  // It returns undefined, or the reason the checker could not be built.
  createTypeChecker() {
    const ts = this.ts;
    try {
      const moduleResolution = ts.ModuleResolutionKind.Bundler ?? ts.ModuleResolutionKind.NodeJs;
      const options = {target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext,moduleResolution,types:[],noEmit:true,skipLibCheck:true,experimentalDecorators:true,strict:true};
      const libraryDirectory = path.dirname(ts.getDefaultLibFilePath(options));
      const sourcePath = path.resolve(this.path);
      const projectRoot = projectRootDirectory(sourcePath);
      const isLibraryFile = fileName => path.dirname(path.resolve(fileName)) === libraryDirectory && path.basename(fileName).startsWith("lib.");
      // A file under a symbolic link or an installed-package directory stays unresolved, so its logical path is its real path.
      const isProjectFile = fileName => {
        const resolved = path.resolve(fileName);
        if (!projectSourceExtensions.some(extension => resolved.endsWith(extension)) || !fs.existsSync(resolved) || !fs.statSync(resolved).isFile()) return false;
        const relative = path.relative(projectRoot,resolved);
        if (relative === "" || relative.startsWith("..") || path.isAbsolute(relative)) return false;
        let current = projectRoot;
        for (const component of relative.split(path.sep)) {
          current = path.join(current,component);
          if (excludedProjectDirectories.has(component) || fs.lstatSync(current).isSymbolicLink()) return false;
        }
        return true;
      };
      this.projectAnalyzers = new Map([[sourcePath,this]]);
      const host = {
        getSourceFile: (fileName,languageVersion) => {
          if (path.resolve(fileName) === sourcePath) return this.tree;
          if (isLibraryFile(fileName)) return fs.existsSync(fileName) ? ts.createSourceFile(fileName,fs.readFileSync(fileName,"utf8"),languageVersion) : undefined;
          return isProjectFile(fileName) ? this.projectAnalyzer(path.resolve(fileName))?.tree : undefined;
        },
        getDefaultLibFileName: compilerOptions => path.join(libraryDirectory,ts.getDefaultLibFileName(compilerOptions)),
        getDefaultLibLocation: () => libraryDirectory,
        writeFile: () => {},
        getCurrentDirectory: () => path.dirname(sourcePath),
        getCanonicalFileName: fileName => fileName,
        useCaseSensitiveFileNames: () => true,
        getNewLine: () => "\n",
        fileExists: fileName => path.resolve(fileName) === sourcePath || (isLibraryFile(fileName) && fs.existsSync(fileName)) || isProjectFile(fileName),
        readFile: fileName => {
          if (path.resolve(fileName) === sourcePath) return this.source;
          return (isLibraryFile(fileName) || isProjectFile(fileName)) && fs.existsSync(fileName) ? fs.readFileSync(fileName,"utf8") : undefined;
        },
      };
      this.program = ts.createProgram({rootNames:[sourcePath],options,host});
      this.checker = this.program.getTypeChecker();
      return undefined;
    } catch (error) {
      return error.message;
    }
  }

  // hasDataInputCodec reports whether a Step decodes its input with a Dex SDK codec, such as jsonCodec(), which yields plain data.
  // A custom codec may decode the input into an object with methods that do I/O.
  hasDataInputCodec(declaration) {
    const ts = this.ts;
    // A base class, a getter, or a constructor may supply a codec dexcli cannot read.
    if (declaration.heritageClauses?.some(clause => clause.token === ts.SyntaxKind.ExtendsKeyword)) return false;
    if (/\binputCodec\b/.test(this.constructorOf(declaration)?.getText(this.tree) ?? "")) return false;
    const members = declaration.members.filter(candidate => candidate.name && this.propertyName(candidate.name) === "inputCodec");
    if (members.length === 0) return true;
    if (members.length !== 1 || !ts.isPropertyDeclaration(members[0])) return false;
    // Only an SDK codec without configuration, such as stringCodec or jsonCodec(), decodes plain data.
    const codec = this.unwrap(members[0].initializer);
    const isUnconfiguredCall = codec && ts.isCallExpression(codec) && codec.arguments.length === 0;
    const codecName = codec && (isUnconfiguredCall ? this.dexName(codec.expression) : ts.isCallExpression(codec) ? undefined : this.dexName(codec));
    return typeof codecName === "string" && codecName !== "*" && /Codec$/.test(codecName);
  }

  // projectAnalyzer returns the analyzer of another project file, which only parses it and indexes its names, or undefined.
  projectAnalyzer(filePath) {
    if (this.projectAnalyzers.has(filePath)) return this.projectAnalyzers.get(filePath);
    // Past the bound, an import stays unresolved, so its calls keep the conservative classification.
    if (this.projectAnalyzers.size > maximumProjectFiles) return undefined;
    let analyzer;
    try {
      analyzer = new TypeScriptAnalyzer({path:filePath,source:fs.readFileSync(filePath,"utf8"),flowType:"",compilerEntryPath:this.compilerEntryPath});
      for (const statement of analyzer.tree.statements) {
        if (analyzer.ts.isImportDeclaration(statement)) analyzer.indexImport(statement);
        if (analyzer.ts.isClassDeclaration(statement) && statement.name) analyzer.classes.set(statement.name.text,statement);
        if (analyzer.ts.isFunctionDeclaration(statement) && statement.name && statement.body) analyzer.functions.set(statement.name.text,statement);
        if (analyzer.ts.isVariableStatement(statement)) for (const declaration of statement.declarationList.declarations) analyzer.indexValue(declaration.name,declaration.initializer);
      }
      analyzer.indexBindings();
      analyzer.projectAnalyzers = this.projectAnalyzers;
    } catch {
      analyzer = undefined;
    }
    this.projectAnalyzers.set(filePath,analyzer);
    return analyzer;
  }

  // projectFileExternalCall follows a function or constructor declared in another project file with that file's analyzer.
  projectFileExternalCall(calleeText,declaration,depth) {
    const ts = this.ts;
    const external = {external:calleeText,isExact:true};
    const sourceFile = declaration.getSourceFile();
    const analyzer = this.projectAnalyzers?.get(path.resolve(sourceFile.fileName));
    if (!analyzer || analyzer.tree !== sourceFile) return external;
    analyzer.program = this.program;
    analyzer.checker = this.checker;
    if (ts.isClassDeclaration(declaration)) return analyzer.classConstructionExternalCall(calleeText,declaration,depth);
    const implementation = analyzer.implementationOf(declaration);
    if (!implementation) return external;
    if (ts.isConstructorDeclaration(implementation)) return analyzer.classConstructionExternalCall(calleeText,implementation.parent,depth);
    const isFunction = ts.isFunctionDeclaration(implementation) ||
      ((ts.isArrowFunction(implementation) || ts.isFunctionExpression(implementation)) && ts.isVariableDeclaration(implementation.parent));
    if (!isFunction || !implementation.body) return external;
    return analyzer.helperExternalCall(calleeText,implementation,[implementation.body],depth);
  }

  // bodyExternalCalls returns the possible external calls in nodes, and whether the result is exact.
  // When firstOnly is set it stops at the first call found.
  bodyExternalCalls(nodes,depth,firstOnly) {
    const scan = {calls:[],isExact:true};
    const visit = node => {
      if (!node || (firstOnly && scan.calls.length) || this.ts.isTypeNode(node)) return;
      let found;
      if (this.ts.isCallExpression(node) || this.ts.isNewExpression(node) || this.ts.isTaggedTemplateExpression(node)) found = this.classifyInvocation(node,depth);
      else if (this.ts.isIdentifier(node) || this.ts.isPropertyAccessExpression(node)) found = this.classifyFunctionValue(node,depth);
      if (found) {
        scan.isExact = scan.isExact && found.isExact;
        if (found.external) scan.calls.push({callee:found.external,span:this.span(node)});
      }
      if (this.ts.isCallExpression(node) || this.ts.isNewExpression(node)) scan.calls.push(...this.callbackArgumentExternalCalls(node));
      this.ts.forEachChild(node,visit);
    };
    for (const node of nodes) visit(node);
    return scan;
  }

  // classifyInvocation returns {external, isExact} for a call, new, or tagged template; external is the callee text or empty.
  classifyInvocation(node,depth) {
    const ts = this.ts;
    const calleeExpression = ts.isTaggedTemplateExpression(node) ? node.tag : node.expression;
    const callee = this.unwrap(calleeExpression);
    const calleeText = this.calleeText(node,calleeExpression);
    const allowed = {external:"",isExact:true};
    const external = {external:calleeText,isExact:true};
    if (!callee || callee.kind === ts.SyntaxKind.ImportKeyword) return external;
    if (ts.isCallExpression(node) && (ts.isArrowFunction(callee) || ts.isFunctionExpression(callee))) return allowed;
    const dexExport = this.dexInvocationExport(callee);
    if (dexExport) return dexExternalExports.has(dexExport) ? external : allowed;
    if (ts.isElementAccessExpression(callee) && !this.memberCall(callee)?.method) return external;
    let declaration;
    try {
      declaration = this.checker.getResolvedSignature(node)?.declaration;
    } catch {
      return external;
    }
    if (!declaration && ts.isNewExpression(node)) {
      const constructed = this.sourceDeclarationOf(ts.isPropertyAccessExpression(callee) ? callee.name : callee);
      if (constructed && ts.isClassDeclaration(constructed)) {
        if (constructed.getSourceFile() !== this.tree) return this.projectFileExternalCall(calleeText,constructed,depth);
        return this.classConstructionExternalCall(calleeText,constructed,depth);
      }
    }
    return this.classifyDeclaration(calleeText,declaration,callee,depth);
  }

  // callbackArgumentExternalCalls returns the callbacks a default-library call, such as map or then, receives
  // that are not function literals, same-file functions, or pure built-ins; the library calls them.
  callbackArgumentExternalCalls(node) {
    const ts = this.ts;
    if (!node.arguments?.length) return [];
    let declaration;
    try {
      declaration = this.checker.getResolvedSignature(node)?.declaration;
    } catch {
      return [];
    }
    if (!declaration?.parameters || !this.program.isSourceFileDefaultLibrary(declaration.getSourceFile())) return [];
    const calls = [];
    for (const [index,argument] of node.arguments.entries()) {
      const parameter = declaration.parameters[index] || (declaration.parameters.at(-1)?.dotDotDotToken ? declaration.parameters.at(-1) : undefined);
      if (!parameter?.type || !this.typeNodeHasFunction(parameter.type,parameter.getSourceFile())) continue;
      if (this.isCheckedCallback(argument)) continue;
      calls.push({callee:this.collapsedText(argument),span:this.span(argument)});
    }
    return calls;
  }

  // isCheckedCallback reports whether a callback argument is checked elsewhere or does no I/O.
  isCheckedCallback(argument) {
    const ts = this.ts;
    const value = this.unwrap(argument);
    if (!value || ts.isArrowFunction(value) || ts.isFunctionExpression(value)) return true;
    if (value.kind === ts.SyntaxKind.NullKeyword || (ts.isIdentifier(value) && value.text === "undefined")) return true;
    if (this.dexValueOrigin(value,0)) return true;
    // A parameter may also accept a non-function, such as the key list of JSON.stringify; an unresolved import is any.
    const type = this.checker.getTypeAtLocation(value);
    if (!(type.flags & (ts.TypeFlags.Any | ts.TypeFlags.Unknown)) && !type.getCallSignatures().length) return true;
    if (!ts.isIdentifier(value) && !ts.isPropertyAccessExpression(value)) return false;
    const declaration = this.sourceDeclarationOf(ts.isPropertyAccessExpression(value) ? value.name : value);
    if (!declaration) return false;
    const sourceFile = declaration.getSourceFile();
    if (this.program.isSourceFileDefaultLibrary(sourceFile)) {
      if (ts.isFunctionDeclaration(declaration)) return pureLibraryFunctions.has(declaration.name?.text);
      return ts.isVariableDeclaration(declaration) && ts.isIdentifier(declaration.name) && pureLibraryContainers.has(declaration.name.text);
    }
    if (sourceFile !== this.tree) return false;
    // classifyFunctionValue follows same-file functions passed as values.
    return ts.isFunctionDeclaration(declaration) || ts.isMethodDeclaration(declaration) ||
      (ts.isVariableDeclaration(declaration) && !!declaration.initializer && (ts.isArrowFunction(this.unwrap(declaration.initializer)) || ts.isFunctionExpression(this.unwrap(declaration.initializer))));
  }

  typeNodeHasFunction(typeNode,sourceFile) {
    const ts = this.ts;
    if (ts.isFunctionTypeNode(typeNode) || ts.isConstructorTypeNode(typeNode)) return true;
    if (ts.isParenthesizedTypeNode(typeNode)) return this.typeNodeHasFunction(typeNode.type,sourceFile);
    if (ts.isUnionTypeNode(typeNode) || ts.isIntersectionTypeNode(typeNode)) return typeNode.types.some(part => this.typeNodeHasFunction(part,sourceFile));
    return ts.isTypeReferenceNode(typeNode) && typeNode.typeName.getText(sourceFile) === "Function";
  }

  // classifyFunctionValue checks a function referenced as a value, such as a callback argument, which may run later.
  classifyFunctionValue(node,depth) {
    const ts = this.ts;
    if (ts.isIdentifier(node) && ts.isPropertyAccessExpression(node.parent) && node.parent.name === node) return undefined;
    if (node.parent && ts.isDeclaration(node.parent) && node.parent.name === node) return undefined;
    let use = node;
    while (this.isTransparentWrapper(use.parent)) use = use.parent;
    const parent = use.parent;
    if (!parent) return undefined;
    if ((ts.isCallExpression(parent) || ts.isNewExpression(parent)) && parent.expression === use) return undefined;
    if (ts.isTaggedTemplateExpression(parent) && parent.tag === use) return undefined;
    if ((ts.isPropertyAccessExpression(parent) || ts.isElementAccessExpression(parent)) && parent.expression === use) return undefined;
    if (ts.isTypeOfExpression(parent)) return undefined;
    const declaration = this.sourceDeclarationOf(ts.isPropertyAccessExpression(node) ? node.name : node);
    if (!declaration) return undefined;
    const isFunction = ts.isFunctionDeclaration(declaration) || ts.isMethodDeclaration(declaration) ||
      (ts.isVariableDeclaration(declaration) && declaration.initializer && (ts.isArrowFunction(this.unwrap(declaration.initializer)) || ts.isFunctionExpression(this.unwrap(declaration.initializer))));
    if (!isFunction) return undefined;
    const calleeText = this.collapsedText(use);
    const target = ts.isVariableDeclaration(declaration) ? this.unwrap(declaration.initializer) : declaration;
    return this.classifyDeclaration(calleeText,target,node,depth);
  }

  // classifyDeclaration classifies a callee by its resolved declaration; no declaration is a dynamic or unresolved call.
  classifyDeclaration(calleeText,declaration,callee,depth) {
    const ts = this.ts;
    const allowed = {external:"",isExact:true};
    const external = {external:calleeText,isExact:true};
    if (!declaration) return external;
    const sourceFile = declaration.getSourceFile();
    if (this.program.isSourceFileDefaultLibrary(sourceFile)) return this.isPureLibraryDeclaration(declaration) ? allowed : external;
    if (sourceFile !== this.tree) return this.projectFileExternalCall(calleeText,declaration,depth);
    declaration = this.implementationOf(declaration);
    if (!declaration) return external;
    if (ts.isConstructorDeclaration(declaration)) return this.classConstructionExternalCall(calleeText,declaration.parent,depth);
    const isClassMember = ts.isMethodDeclaration(declaration) || ts.isGetAccessor(declaration) || ts.isSetAccessor(declaration) ||
      ((ts.isArrowFunction(declaration) || ts.isFunctionExpression(declaration)) && ts.isPropertyDeclaration(declaration.parent));
    if (isClassMember) {
      const owner = ts.isPropertyDeclaration(declaration.parent) ? declaration.parent.parent : declaration.parent;
      // Another object's method may be an injected implementation, so only calls on this or a proven same-file object are followed.
      if (!ts.isClassDeclaration(owner) || !this.isProvenSameFileReceiver(callee)) return external;
      return this.helperExternalCall(calleeText,declaration,[declaration.body],depth);
    }
    const isFunction = ts.isFunctionDeclaration(declaration) ||
      ((ts.isArrowFunction(declaration) || ts.isFunctionExpression(declaration)) && ts.isVariableDeclaration(declaration.parent));
    if (!isFunction || !declaration.body) return external;
    return this.helperExternalCall(calleeText,declaration,[declaration.body],depth);
  }

  // implementationOf returns the declaration with a body for an overload signature, or undefined when there is none.
  implementationOf(declaration) {
    if (declaration.body) return declaration;
    const sibling = declaration.symbol?.declarations?.find(candidate => candidate.kind === declaration.kind && candidate.body);
    return sibling;
  }

  // isProvenSameFileReceiver reports whether a member callee runs on this, super, a class, or a same-file object the analyzer proves.
  isProvenSameFileReceiver(callee) {
    const ts = this.ts;
    if (!ts.isPropertyAccessExpression(callee) && !ts.isElementAccessExpression(callee)) return false;
    const receiver = this.unwrap(callee.expression);
    if (!receiver) return false;
    if (receiver.kind === ts.SyntaxKind.ThisKeyword || receiver.kind === ts.SyntaxKind.SuperKeyword) return true;
    const receiverDeclaration = ts.isIdentifier(receiver) ? this.sourceDeclarationOf(receiver) : undefined;
    if (receiverDeclaration && ts.isClassDeclaration(receiverDeclaration)) return true;
    const value = this.resolvedValue(receiver,this.currentFrame).node;
    if (value?.kind === ts.SyntaxKind.ThisKeyword) return !!this.selfReferenceClass(value);
    return !!value && ts.isNewExpression(value) && !!this.sourceDeclarationOf(this.unwrap(value.expression)) && ts.isClassDeclaration(this.sourceDeclarationOf(this.unwrap(value.expression)));
  }

  // classConstructionExternalCall checks the constructor, instance field initializers, and base class of a same-file class.
  classConstructionExternalCall(calleeText,declaration,depth) {
    const ts = this.ts;
    const constructor = this.constructorOf(declaration);
    const base = this.extendsClause(declaration);
    if (!constructor && base) {
      const baseDeclaration = this.sourceDeclarationOf(this.unwrap(base.expression));
      if (!baseDeclaration || !ts.isClassDeclaration(baseDeclaration)) return {external:calleeText,isExact:true};
    }
    const nodes = declaration.members.filter(member => ts.isPropertyDeclaration(member) && !this.isStatic(member) && member.initializer).map(member => member.initializer);
    if (constructor) nodes.push(constructor.body);
    const result = this.helperExternalCall(calleeText,declaration,nodes,depth);
    if (result.external || constructor || !base) return result;
    const baseResult = this.classConstructionExternalCall(calleeText,this.sourceDeclarationOf(this.unwrap(base.expression)),depth + 1);
    return {external:baseResult.external,isExact:result.isExact && baseResult.isExact};
  }

  // helperExternalCall follows a same-file helper and reports its first possible external call, as "helper (calls inner)".
  helperExternalCall(calleeText,key,nodes,depth) {
    let inner;
    let isExact = true;
    if (this.exactHelperResults.has(key)) inner = this.exactHelperResults.get(key);
    else if (this.helpersInProgress.has(key)) return {external:"",isExact:false};
    else if (depth + 1 > maximumHelperDepth) return {external:`${calleeText} (calls a helper chain deeper than dexcli follows)`,isExact:false};
    else {
      this.helpersInProgress.add(key);
      const scan = this.bodyExternalCalls(nodes,depth + 1,true);
      this.helpersInProgress.delete(key);
      inner = scan.calls[0]?.callee || "";
      isExact = scan.isExact;
      if (isExact) this.exactHelperResults.set(key,inner);
    }
    return {external:inner ? `${calleeText} (calls ${inner})` : "",isExact};
  }

  // dexInvocationExport returns the Dex SDK export or pure Node export a callee calls or calls a method of, or undefined.
  dexInvocationExport(callee) {
    const ts = this.ts;
    const named = this.allowedRootName(callee);
    if (named) return named;
    if (!ts.isPropertyAccessExpression(callee) && !ts.isElementAccessExpression(callee)) return undefined;
    const receiver = this.unwrap(callee.expression);
    if (this.accessedResource(receiver) || this.resourceFor(receiver)) return "resource";
    if (this.isDexContextValue(receiver)) return "Context";
    return this.dexValueOrigin(receiver,0);
  }

  // dexValueOrigin returns the Dex SDK or pure Node export a value comes from, such as Attribute or Context, or undefined.
  // A property, element, or method result of such a value comes from it too.
  dexValueOrigin(expression,depth) {
    const ts = this.ts;
    const value = this.unwrap(expression);
    if (!value || depth > 2 * maximumHelperDepth) return undefined;
    const named = this.allowedRootName(value);
    if (named) return named;
    if (ts.isIdentifier(value)) {
      const declaration = this.sourceDeclarationOf(value);
      return declaration ? this.declarationDexOrigin(declaration,depth + 1) : undefined;
    }
    if (ts.isPropertyAccessExpression(value)) {
      const symbol = this.checker.getSymbolAtLocation(value.name);
      if (!symbol) return this.dexValueOrigin(value.expression,depth + 1);
      const declaration = symbol.valueDeclaration;
      return declaration && declaration.getSourceFile() === this.tree ? this.declarationDexOrigin(declaration,depth + 1) : undefined;
    }
    if (ts.isElementAccessExpression(value)) return this.dexValueOrigin(value.expression,depth + 1);
    if (ts.isBinaryExpression(value) && fallbackOperatorKinds(ts).has(value.operatorToken.kind)) return this.alternativesDexOrigin([value.left,value.right],depth + 1);
    if (ts.isConditionalExpression(value)) return this.alternativesDexOrigin([value.whenTrue,value.whenFalse],depth + 1);
    if (ts.isNewExpression(value)) return this.allowedRootName(this.unwrap(value.expression));
    if (ts.isCallExpression(value)) {
      const callee = this.unwrap(value.expression);
      if (!callee) return undefined;
      if (ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee)) return this.allowedRootName(callee) || this.dexValueOrigin(callee.expression,depth + 1);
      return this.allowedRootName(callee);
    }
    return undefined;
  }

  // alternativesDexOrigin returns the origin of a fallback, such as attribute.get(context) ?? empty, when every branch is a Dex value or literal data.
  // A branch that reaches a Dex client or Worker decides the origin, so its calls stay external.
  alternativesDexOrigin(alternatives,depth) {
    let origin;
    for (const alternative of alternatives) {
      const alternativeOrigin = this.dexValueOrigin(alternative,depth);
      if (alternativeOrigin && dexExternalExports.has(alternativeOrigin)) return alternativeOrigin;
      if (alternativeOrigin) origin = origin || alternativeOrigin;
      else if (!this.isLiteralDataValue(alternative,depth)) return undefined;
    }
    return origin;
  }

  // isLiteralDataValue reports whether a value is a literal written inline, or a provably data const; neither holds a service.
  isLiteralDataValue(expression,depth) {
    const ts = this.ts;
    const value = this.unwrap(expression);
    if (!value || depth > 2 * maximumHelperDepth) return false;
    if (ts.isStringLiteral(value) || ts.isNumericLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value) || ts.isBigIntLiteral(value)) return true;
    if ([ts.SyntaxKind.TrueKeyword,ts.SyntaxKind.FalseKeyword,ts.SyntaxKind.NullKeyword].includes(value.kind)) return true;
    if (ts.isIdentifier(value) && value.text === "undefined") return true;
    if (ts.isPrefixUnaryExpression(value) && [ts.SyntaxKind.MinusToken,ts.SyntaxKind.PlusToken].includes(value.operator)) return ts.isNumericLiteral(value.operand) || ts.isBigIntLiteral(value.operand);
    if (ts.isTemplateExpression(value)) return value.templateSpans.every(span => this.isLiteralDataValue(span.expression,depth + 1));
    if (ts.isArrayLiteralExpression(value)) return value.elements.every(element => this.isLiteralDataValue(ts.isSpreadElement(element) ? element.expression : element,depth + 1));
    if (ts.isObjectLiteralExpression(value)) {
      return value.properties.every(property => ts.isPropertyAssignment(property) ? !ts.isComputedPropertyName(property.name) && this.isLiteralDataValue(property.initializer,depth + 1) :
        ts.isShorthandPropertyAssignment(property) ? this.isLiteralDataValue(property.name,depth + 1) :
        ts.isSpreadAssignment(property) && this.isLiteralDataValue(property.expression,depth + 1));
    }
    return ts.isIdentifier(value) && this.isProvablyDataConst(value,depth + 1);
  }

  // isProvablyDataConst reports whether a name is a same-file const with a literal initializer and a data type.
  // A typed binding of another origin, such as a narrow interface holding a service, never counts.
  isProvablyDataConst(identifier,depth) {
    const ts = this.ts;
    const declaration = this.sourceDeclarationOf(identifier);
    if (!declaration || !ts.isVariableDeclaration(declaration) || !declaration.initializer || declaration.getSourceFile() !== this.tree) return false;
    if (!ts.isVariableDeclarationList(declaration.parent) || (declaration.parent.flags & ts.NodeFlags.Const) === 0) return false;
    if (!this.isLiteralDataValue(declaration.initializer,depth + 1)) return false;
    try {
      // The use site keeps control-flow narrowing, such as "send" in value.
      return this.isDataType(this.checker.getTypeAtLocation(declaration.name),new Set(),0) &&
        this.isDataType(this.checker.getTypeAtLocation(identifier),new Set(),0);
    } catch {
      return false;
    }
  }

  // isDataType reports whether a checked type contains no function, any, unknown, or unresolved part.
  isDataType(type,visited,depth) {
    const ts = this.ts;
    if (visited.has(type)) return true;
    if (depth > maximumDataTypeDepth) return false;
    visited.add(type);
    const flags = type.flags;
    if (flags & (ts.TypeFlags.Any | ts.TypeFlags.Unknown | ts.TypeFlags.TypeParameter | ts.TypeFlags.NonPrimitive | ts.TypeFlags.Never)) return false;
    if (type.isUnionOrIntersection()) return type.types.every(part => this.isDataType(part,visited,depth + 1));
    if (!(flags & ts.TypeFlags.Object)) return !!(flags & (ts.TypeFlags.StringLike | ts.TypeFlags.NumberLike | ts.TypeFlags.BigIntLike | ts.TypeFlags.BooleanLike | ts.TypeFlags.EnumLike | ts.TypeFlags.ESSymbolLike | ts.TypeFlags.Null | ts.TypeFlags.Undefined | ts.TypeFlags.Void));
    if (type.getCallSignatures().length || type.getConstructSignatures().length) return false;
    const libraryName = type.symbol?.declarations?.some(declaration => this.program.isSourceFileDefaultLibrary(declaration.getSourceFile())) ? type.symbol.name : undefined;
    if (this.checker.isTupleType(type) || (libraryName && dataContainerTypeNames.has(libraryName))) {
      return this.checker.getTypeArguments(type).every(argument => this.isDataType(argument,visited,depth + 1));
    }
    if (libraryName && dataLibraryTypeNames.has(libraryName)) return true;
    const location = this.tree;
    // A getter runs code when read.
    return this.checker.getPropertiesOfType(type).every(property => !(property.flags & ts.SymbolFlags.GetAccessor) && this.isDataType(this.checker.getTypeOfSymbolAtLocation(property,location),visited,depth + 1)) &&
      this.checker.getIndexInfosOfType(type).every(info => this.isDataType(info.type,visited,depth + 1));
  }

  // allowedRootName returns the Dex SDK export or pure Node export a name refers to, or undefined.
  allowedRootName(expression) {
    return expression ? this.dexName(expression) || this.pureNodeExport(expression) : undefined;
  }

  // pureNodeExport returns module.export for an allowlisted Node export, or Buffer for the unbound Buffer global.
  pureNodeExport(expression) {
    const ts = this.ts;
    if (ts.isPropertyAccessExpression(expression)) {
      const namespace = this.unwrap(expression.expression);
      const imported = namespace && ts.isIdentifier(namespace) ? this.nodeImportOf(namespace) : undefined;
      return imported && !imported.exportName && pureNodeExports.get(imported.moduleName)?.has(expression.name.text) ? `${imported.moduleName}.${expression.name.text}` : undefined;
    }
    if (!ts.isIdentifier(expression)) return undefined;
    if (expression.text === "Buffer" && !this.checker.getSymbolAtLocation(expression)) return "Buffer";
    const imported = this.nodeImportOf(expression);
    return imported?.exportName && pureNodeExports.get(imported.moduleName)?.has(imported.exportName) ? `${imported.moduleName}.${imported.exportName}` : undefined;
  }

  // nodeImportOf returns the module and export name an identifier imports, or undefined; a namespace or default import has no export name.
  nodeImportOf(identifier) {
    const ts = this.ts;
    const declaration = this.checker.getSymbolAtLocation(identifier)?.declarations?.[0];
    let statement = declaration;
    while (statement && !ts.isImportDeclaration(statement)) statement = statement.parent;
    if (!statement || statement.importClause?.isTypeOnly || !ts.isStringLiteral(statement.moduleSpecifier)) return undefined;
    const moduleName = statement.moduleSpecifier.text.replace(/^node:/,"");
    if (ts.isImportSpecifier(declaration)) return {moduleName,exportName:(declaration.propertyName || declaration.name).text};
    return ts.isNamespaceImport(declaration) || ts.isImportClause(declaration) ? {moduleName} : undefined;
  }

  declarationDexOrigin(declaration,depth) {
    const ts = this.ts;
    // The Step input is decoded data, so it holds no service.
    if (declaration === this.stepInputParameter) return "StepInput";
    if (ts.isBindingElement(declaration)) {
      let root = declaration.parent;
      while (root && (ts.isObjectBindingPattern(root) || ts.isArrayBindingPattern(root) || ts.isBindingElement(root))) root = root.parent;
      return root ? this.declarationDexOrigin(root,depth + 1) : undefined;
    }
    if (!ts.isParameter(declaration) && !ts.isVariableDeclaration(declaration) && !ts.isPropertyDeclaration(declaration) && !ts.isPropertySignature(declaration)) return undefined;
    if (declaration.type) return this.typeNodeDexOrigin(declaration.type,new Set());
    if (declaration.initializer) return this.dexValueOrigin(declaration.initializer,depth + 1);
    // An untyped callback parameter, such as the item of results.map(...), comes from the call's receiver.
    const callback = declaration.parent;
    if (ts.isParameter(declaration) && callback && (ts.isArrowFunction(callback) || ts.isFunctionExpression(callback)) && ts.isCallExpression(callback.parent) && callback.parent.arguments.includes(callback)) {
      const callee = this.unwrap(callback.parent.expression);
      if (callee && (ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee))) return this.dexValueOrigin(callee.expression,depth + 1);
    }
    return undefined;
  }

  typeNodeDexOrigin(typeNode,seen) {
    const ts = this.ts;
    if (!typeNode || seen.has(typeNode)) return undefined;
    seen.add(typeNode);
    if (ts.isParenthesizedTypeNode(typeNode) || ts.isTypeOperatorNode(typeNode)) return this.typeNodeDexOrigin(typeNode.type,seen);
    if (ts.isArrayTypeNode(typeNode)) return this.typeNodeDexOrigin(typeNode.elementType,seen);
    if (ts.isUnionTypeNode(typeNode) || ts.isIntersectionTypeNode(typeNode)) {
      for (const part of typeNode.types) {
        const origin = this.typeNodeDexOrigin(part,seen);
        if (origin) return origin;
      }
      return undefined;
    }
    if (!ts.isTypeReferenceNode(typeNode)) return undefined;
    const name = typeNode.typeName;
    if (ts.isQualifiedName(name)) return this.imports.get(name.left.getText(this.tree)) === "*" ? name.right.text : undefined;
    const imported = this.imports.get(name.text);
    if (imported) return imported;
    const alias = this.inputTypes.get(name.text);
    return alias && ts.isTypeAliasDeclaration(alias) ? this.typeNodeDexOrigin(alias.type,seen) : undefined;
  }

  // sourceDeclarationOf returns the value declaration a name resolves to, through import aliases, or undefined.
  sourceDeclarationOf(node) {
    if (!node) return undefined;
    let symbol;
    try {
      symbol = this.checker.getSymbolAtLocation(node);
      if (symbol && symbol.flags & this.ts.SymbolFlags.Alias) symbol = this.checker.getAliasedSymbol(symbol);
    } catch {
      return undefined;
    }
    return symbol?.valueDeclaration || symbol?.declarations?.[0];
  }

  // isPureLibraryDeclaration reports whether a default-library declaration belongs to an allowlisted container or function.
  isPureLibraryDeclaration(declaration) {
    const ts = this.ts;
    if (ts.isFunctionDeclaration(declaration) && ts.isSourceFile(declaration.parent)) return pureLibraryFunctions.has(declaration.name?.text);
    for (let current = declaration.parent; current && !ts.isSourceFile(current); current = current.parent) {
      let name;
      if (ts.isInterfaceDeclaration(current) || ts.isClassDeclaration(current) || ts.isModuleDeclaration(current)) name = current.name?.text;
      else if (ts.isTypeLiteralNode(current) && ts.isVariableDeclaration(current.parent)) name = current.parent.name.getText(current.getSourceFile());
      // Members of an allowlisted namespace, such as Intl.NumberFormat, are allowed too.
      if (name !== undefined && (pureLibraryContainers.has(name) || pureLibraryContainerPattern.test(name))) return true;
    }
    return false;
  }

  calleeText(node,calleeExpression) {
    const text = this.collapsedText(calleeExpression);
    return this.ts.isNewExpression(node) ? `new ${text}` : text;
  }

  collapsedText(node) { return node.getText(this.tree).replace(/\s+/g," ").replace(/ ?(\??\.) ?/g,"$1"); }

  attributeFact(node) {
    const declaration = this.attributeDeclarations.get(node.id);
    const annotation = declaration && this.resourceTypeAnnotation(declaration);
    const valueTypeUnresolved = this.isImportedAttributeType(annotation);
    const valueType = valueTypeUnresolved ? "json" : this.attributeValueType(node.resource.valueType);
    const fact = {resourceId:node.id,key:node.name,valueType,valueTypeUnresolved,isMap:node.resource.map};
    if (!declaration) return fact;
    const anchor = this.ts.findAncestor(declaration,parent => this.ts.isVariableStatement(parent) || this.ts.isPropertyDeclaration(parent) || this.ts.isExpressionStatement(parent));
    fact.directives = this.directiveLines(anchor || declaration,"indexed-attribute");
    fact.span = node.span;
    const index = this.resolvedValue(declaration.arguments?.[2],{}).node;
    if (!index || index.kind === this.ts.SyntaxKind.NullKeyword || (this.ts.isIdentifier(index) && index.text === "undefined")) return fact;
    fact.isIndexed = true;
    fact.indexType = "unknown";
    if (!this.ts.isObjectLiteralExpression(index)) return fact;
    const properties = new Map(index.properties.filter(property => property.name).map(property => [this.propertyName(property.name),this.propertyValueExpression(property)]));
    const indexKey = this.staticLiteral(properties.get("indexKey"));
    fact.indexKey = typeof indexKey === "string" && indexKey ? indexKey : node.resource.map ? "" : node.name;
    const indexType = this.resolvedValue(properties.get("type"),{}).node;
    let indexTypeName = this.staticLiteral(indexType);
    if (indexType && this.ts.isPropertyAccessExpression(indexType) && this.dexName(indexType.expression) === "IndexType") indexTypeName = indexType.name.text;
    fact.indexType = {KEYWORD:"keyword",FULL_TEXT:"fulltext",KEYWORD_ARRAY:"keyword-array",INT:"int",DOUBLE:"double",BOOL:"bool",DATETIME:"datetime",keyword:"keyword",fullText:"fulltext",keywordArray:"keyword-array",int:"int",double:"double",bool:"bool",datetime:"datetime"}[indexTypeName] || "unknown";
    return fact;
  }

  attributeValueType(text) {
    const type = text.replace(/\s/g,"");
    if (["string[]","readonlystring[]","Array<string>","ReadonlyArray<string>"].includes(type)) return "string-array";
    if (type === "Uint8Array" || type.endsWith("[]") || /^(?:Readonly)?Array</.test(type)) return "array";
    if (type.startsWith("{") || type.startsWith("Record<")) return "object";
    return {string:"string",number:"double",bigint:"int64",boolean:"bool",Date:"datetime"}[type] || "json";
  }

  isImportedAttributeType(annotation) {
    if (!annotation || !this.ts.isTypeReferenceNode(annotation)) return false;
    let root = annotation.typeName;
    while (this.ts.isQualifiedName(root)) root = root.left;
    const scope = this.bindingScope(root);
    const binding = scope && this.staticBinding(scope,root.text);
    return !!binding && (this.ts.isImportSpecifier(binding) || this.ts.isImportClause(binding) ||
      this.ts.isNamespaceImport(binding) || this.ts.isImportEqualsDeclaration(binding));
  }
}

class StartInputExtractionError extends Error {}

try {
  process.stdout.write(JSON.stringify(new TypeScriptAnalyzer(JSON.parse(fs.readFileSync(0,"utf8"))).analyze()));
} catch (error) {
  process.stderr.write(error.message);
  process.exitCode = 1;
}
