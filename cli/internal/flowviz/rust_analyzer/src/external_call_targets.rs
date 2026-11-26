// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use syn::{GenericArgument, Generics, Item, PathArguments, Type, UseTree};

use crate::{RustFlowSource, associated_type, type_name};

/// Standard-library modules whose items do no I/O and do not wait on other threads.
const PURE_STANDARD_LIBRARY_MODULES: &[&str] = &[
    "alloc",
    "any",
    "array",
    "ascii",
    "borrow",
    "boxed",
    "cell",
    "char",
    "clone",
    "cmp",
    "collections",
    "convert",
    "default",
    "error",
    "f32",
    "f64",
    "fmt",
    "hash",
    "hint",
    "i8",
    "i16",
    "i32",
    "i64",
    "i128",
    "isize",
    "iter",
    "marker",
    "mem",
    "num",
    "ops",
    "option",
    "primitive",
    "rc",
    "result",
    "slice",
    "str",
    "string",
    "sync",
    "time",
    "u8",
    "u16",
    "u32",
    "u64",
    "u128",
    "usize",
    "vec",
];

/// Exceptions inside the pure modules: they wait on another thread.
const BLOCKING_STANDARD_LIBRARY_PATHS: &[&str] = &["sync::mpsc", "sync::Barrier", "sync::Condvar"];

/// Names the Rust prelude brings into scope, including primitive types.
const PRELUDE_NAMES: &[&str] = &[
    "AsMut",
    "AsRef",
    "Box",
    "Clone",
    "Default",
    "Eq",
    "Err",
    "From",
    "Into",
    "IntoIterator",
    "Iterator",
    "None",
    "Ok",
    "Option",
    "Ord",
    "PartialEq",
    "PartialOrd",
    "Result",
    "Some",
    "String",
    "ToOwned",
    "ToString",
    "TryFrom",
    "TryInto",
    "Vec",
    "bool",
    "char",
    "f32",
    "f64",
    "i8",
    "i16",
    "i32",
    "i64",
    "i128",
    "isize",
    "str",
    "u8",
    "u16",
    "u32",
    "u64",
    "u128",
    "usize",
];

/// Standard-library wrappers whose methods reach the wrapped value.
const TRANSPARENT_WRAPPER_TYPES: &[&str] = &["Arc", "Box", "Cow", "LazyLock", "OnceLock", "Rc"];

/// Option, Result, and the Dex SDK Result aliases: their payload is the first type argument.
const OPTIONAL_TYPES: &[&str] = &["HandlerResult", "Option", "Result", "SdkResult"];

/// Standard traits whose `impl Trait` values only convert or format data.
const CONVERSION_TRAITS: &[&str] = &["AsRef", "Borrow", "Debug", "Display", "Into", "ToString"];

/// Standard collections: their own methods do no I/O, whatever their elements are.
const COLLECTION_TYPES: &[&str] = &[
    "BTreeMap",
    "BTreeSet",
    "BinaryHeap",
    "HashMap",
    "HashSet",
    "LinkedList",
    "Vec",
    "VecDeque",
];

/// Dex SDK items that reach the Dex server, other Flows, or the file system.
const DEX_SDK_EXTERNAL_ITEMS: &[&str] = &["BlobCache", "Client", "FileCredential", "Worker"];

/// serde_json functions that read or write through an I/O reader or writer.
const SERDE_JSON_EXTERNAL_FUNCTIONS: &[&str] = &["from_reader", "to_writer", "to_writer_pretty"];

/// Dex SDK types that store application data; their first type argument is the value type.
const DEX_DATA_RESOURCE_TYPES: &[&str] = &[
    "Attribute",
    "AttributeMap",
    "Channel",
    "ChannelMap",
    "Stream",
];

/// Methods a derived standard trait adds, by trait; derived impls only copy or build fields.
const DERIVED_TRAIT_METHODS: &[(&str, &str)] = &[("Clone", "clone"), ("Default", "default")];

/// Standard-like pure methods allowed on deserialized data: String, str, numbers, collections, Option, Result, and common traits.
pub(crate) const DATA_PURE_METHOD_NAMES: &[&str] = &[
    "abs",
    "all",
    "and_then",
    "any",
    "as_bytes",
    "as_deref",
    "as_mut",
    "as_ref",
    "as_slice",
    "as_str",
    "borrow",
    "bytes",
    "chars",
    "checked_add",
    "checked_div",
    "checked_mul",
    "checked_sub",
    "clamp",
    "clear",
    "clone",
    "cloned",
    "cmp",
    "collect",
    "contains",
    "contains_key",
    "copied",
    "count",
    "default",
    "ends_with",
    "entry",
    "enumerate",
    "eq",
    "expect",
    "extend",
    "filter",
    "filter_map",
    "find",
    "first",
    "flat_map",
    "fmt",
    "fold",
    "get",
    "get_mut",
    "hash",
    "insert",
    "into",
    "into_iter",
    "is_empty",
    "is_err",
    "is_none",
    "is_ok",
    "is_some",
    "iter",
    "iter_mut",
    "join",
    "keys",
    "last",
    "len",
    "lines",
    "map",
    "map_err",
    "map_or",
    "max",
    "min",
    "ne",
    "ok",
    "ok_or",
    "ok_or_else",
    "or_default",
    "or_insert",
    "parse",
    "partial_cmp",
    "pop",
    "pow",
    "push",
    "push_str",
    "remove",
    "replace",
    "retain",
    "rev",
    "saturating_add",
    "saturating_sub",
    "sort",
    "split",
    "split_once",
    "starts_with",
    "strip_prefix",
    "strip_suffix",
    "sum",
    "take",
    "to_lowercase",
    "to_owned",
    "to_string",
    "to_uppercase",
    "to_vec",
    "trim",
    "trim_end",
    "trim_start",
    "unwrap",
    "unwrap_or",
    "unwrap_or_default",
    "unwrap_or_else",
    "values",
];

/// Macros that do no I/O; their arguments are still checked.
pub(crate) const PURE_MACROS: &[&str] = &[
    "assert",
    "assert_eq",
    "assert_ne",
    "cfg",
    "column",
    "concat",
    "debug_assert",
    "debug_assert_eq",
    "debug_assert_ne",
    "env",
    "file",
    "format",
    "format_args",
    "json",
    "line",
    "matches",
    "module_path",
    "option_env",
    "panic",
    "stringify",
    "unreachable",
    "vec",
];

/// tracing and log macros; the Worker owns their output.
pub(crate) const LOG_MACROS: &[&str] = &[
    "debug",
    "debug_span",
    "error",
    "error_span",
    "event",
    "info",
    "info_span",
    "log",
    "span",
    "trace",
    "trace_span",
    "warn",
    "warn_span",
];

/// Macros that format into their first argument.
pub(crate) const WRITE_MACROS: &[&str] = &["write", "writeln"];

/// Macros that write to the process's standard output or error.
pub(crate) const PRINT_MACROS: &[&str] = &["dbg", "eprint", "eprintln", "print", "println"];

/// What a path names.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum PathTarget {
    DexSdk,
    /// A Dex SDK item that reaches the Dex server, other Flows, or the file system.
    DexSdkExternal,
    PureStandardLibrary,
    ExternalStandardLibrary,
    /// An item declared in this file.
    FileItem(String),
    SelfType,
    External,
}

/// How sure the analyzer is that an external call does I/O.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum Certainty {
    /// An unresolved item, such as one from another file, that may be pure.
    Possible,
    /// A Dex Client, standard-library I/O, printing, or an injected trait object.
    Definite,
}

/// Where a value comes from, which decides whether its methods may do I/O.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum ValueOrigin {
    DexSdk,
    PureStandardLibrary,
    /// A value of a type declared in this file.
    FileType(String),
    External(Certainty),
    /// A standard collection; holds its element origin.
    Collection(Box<ValueOrigin>),
    /// An Option or Result; holds its payload origin.
    Optional(Box<ValueOrigin>),
    /// Deserialized data, such as the Step input; it cannot hold a service.
    Data(DataType),
    Unknown,
}

/// The type of a deserialized data value, which decides which of its methods are allowed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum DataType {
    /// A type declared in this file; its methods are checked like any same-file method.
    FileType(String),
    /// A named type from another file or crate; its methods may do I/O.
    Custom,
    /// A field or element of data whose type dexcli cannot resolve; standard-like methods are allowed.
    Unresolved,
}

impl ValueOrigin {
    pub(crate) fn merge(self, other: Self) -> Self {
        match (self, other) {
            (Self::External(left), Self::External(right)) => Self::External(left.max(right)),
            (Self::External(certainty), _) | (_, Self::External(certainty)) => {
                Self::External(certainty)
            }
            (left, right) if left == right => left,
            (Self::Data(_), Self::Data(_)) => Self::Data(DataType::Custom),
            _ => Self::Unknown,
        }
    }

    /// Returns the origin of a deserialized value of this declared type, which holds no service.
    pub(crate) fn into_data(self) -> Self {
        match self {
            Self::FileType(owner) => Self::Data(DataType::FileType(owner)),
            Self::External(_) => Self::Data(DataType::Custom),
            Self::Unknown => Self::Data(DataType::Unresolved),
            Self::Collection(element) => Self::Collection(Box::new(element.into_data())),
            Self::Optional(payload) => Self::Optional(Box::new(payload.into_data())),
            data_or_pure => data_or_pure,
        }
    }

    /// Returns the most certain External element, and PureStandardLibrary otherwise.
    pub(crate) fn container(elements: impl IntoIterator<Item = Self>) -> Self {
        elements
            .into_iter()
            .filter_map(|element| match element {
                Self::External(certainty) => Some(certainty),
                _ => None,
            })
            .max()
            .map_or(Self::PureStandardLibrary, Self::External)
    }

    /// Returns PureStandardLibrary for a collection of pure elements, and a Collection otherwise.
    pub(crate) fn collection(element: Self) -> Self {
        match element {
            Self::PureStandardLibrary | Self::DexSdk => Self::PureStandardLibrary,
            element => Self::Collection(Box::new(element)),
        }
    }

    /// Returns the payload of an Option or Result, as the `?` operator or `unwrap` does.
    pub(crate) fn unwrapped(self) -> Self {
        match self {
            Self::Optional(payload) => *payload,
            other => other,
        }
    }
}

/// The type parameters and Self type that resolve type names inside one item.
#[derive(Clone, Debug, Default)]
pub(crate) struct TypeScope {
    pub(crate) owner: String,
    pub(crate) generics: BTreeSet<String>,
}

impl TypeScope {
    pub(crate) fn new<'generics>(
        owner: &str,
        generics: impl IntoIterator<Item = &'generics Generics>,
    ) -> Self {
        let generics = generics
            .into_iter()
            .flat_map(|generics| generics.type_params())
            .map(|parameter| parameter.ident.to_string())
            .collect();
        Self {
            owner: owner.to_owned(),
            generics,
        }
    }
}

impl RustFlowSource<'_> {
    pub(crate) fn index_use_paths(&mut self, tree: &UseTree, prefix: &mut Vec<String>) {
        match tree {
            UseTree::Path(path) => {
                prefix.push(path.ident.to_string());
                self.index_use_paths(&path.tree, prefix);
                prefix.pop();
            }
            UseTree::Name(name) => {
                self.insert_use_path(name.ident.to_string(), &name.ident, prefix)
            }
            UseTree::Rename(alias) => {
                self.insert_use_path(alias.rename.to_string(), &alias.ident, prefix);
            }
            UseTree::Glob(_) => self.glob_imports.push(prefix.clone()),
            UseTree::Group(group) => {
                for item in &group.items {
                    self.index_use_paths(item, prefix);
                }
            }
        }
    }

    fn insert_use_path(&mut self, local_name: String, imported: &syn::Ident, prefix: &[String]) {
        let mut full_path = prefix.to_vec();
        if imported == "self" {
            let Some(module) = prefix.last() else {
                return;
            };
            let local_name = if local_name == "self" {
                module.clone()
            } else {
                local_name
            };
            self.use_paths.insert(local_name, full_path);
            return;
        }
        full_path.push(imported.to_string());
        self.use_paths.insert(local_name, full_path);
    }

    pub(crate) fn path_target(&self, segments: &[String], scope: &TypeScope) -> PathTarget {
        let mut segments = segments.to_vec();
        for _ in 0..8 {
            let Some(first) = segments.first() else {
                return PathTarget::External;
            };
            match first.as_str() {
                "Self" => return PathTarget::SelfType,
                "crate" | "self" | "super" => {
                    return match segments.get(1) {
                        Some(name) if self.is_file_item(name) => PathTarget::FileItem(name.clone()),
                        _ => PathTarget::External,
                    };
                }
                "std" | "core" | "alloc" => return standard_library_target(&segments[1..]),
                "dex_sdk" => return dex_sdk_target(&segments),
                "serde_json" => {
                    let is_io = segments
                        .last()
                        .is_some_and(|name| SERDE_JSON_EXTERNAL_FUNCTIONS.contains(&name.as_str()));
                    return if is_io {
                        PathTarget::ExternalStandardLibrary
                    } else {
                        PathTarget::PureStandardLibrary
                    };
                }
                "serde" | "tracing" | "log" => return PathTarget::PureStandardLibrary,
                name if scope.generics.contains(name) => return PathTarget::External,
                name => {
                    if let Some(full_path) = self.use_paths.get(name) {
                        let mut resolved = full_path.clone();
                        resolved.extend_from_slice(&segments[1..]);
                        segments = resolved;
                        continue;
                    }
                    if self.is_file_item(name) {
                        return PathTarget::FileItem(name.to_owned());
                    }
                    if PRELUDE_NAMES.contains(&name) {
                        return PathTarget::PureStandardLibrary;
                    }
                    if self.has_only_dex_sdk_glob() {
                        let mut resolved = vec!["dex_sdk".to_owned()];
                        resolved.extend(segments);
                        return dex_sdk_target(&resolved);
                    }
                    return PathTarget::External;
                }
            }
        }
        PathTarget::External
    }

    fn is_file_item(&self, name: &str) -> bool {
        self.declarations.contains_key(name) || self.constants.contains_key(name)
    }

    /// Reports whether a glob import from dex_sdk is the only glob that can supply unknown names.
    fn has_only_dex_sdk_glob(&self) -> bool {
        let roots: Vec<_> = self
            .glob_imports
            .iter()
            .filter_map(|path| path.first().map(String::as_str))
            .filter(|root| !matches!(*root, "std" | "core" | "alloc"))
            .collect();
        !roots.is_empty() && roots.iter().all(|root| *root == "dex_sdk")
    }

    /// Returns the value type of a same-file Attribute, Channel, or Stream static, such as T in `LazyLock<Attribute<T>>`.
    pub(crate) fn resource_value_type(&self, name: &str) -> Option<&Type> {
        let (declared, _) = self.constants.get(name)?;
        let mut current: &Type = declared;
        for _ in 0..8 {
            let Type::Path(path) = current else {
                return None;
            };
            let segment = path.path.segments.last()?;
            let PathArguments::AngleBracketed(arguments) = &segment.arguments else {
                return None;
            };
            let first = arguments.args.iter().find_map(|argument| match argument {
                GenericArgument::Type(argument) => Some(argument),
                _ => None,
            })?;
            if DEX_DATA_RESOURCE_TYPES.contains(&segment.ident.to_string().as_str()) {
                return Some(first);
            }
            current = first;
        }
        None
    }

    /// Returns every field type of a same-file struct or enum, and its generics.
    pub(crate) fn declared_field_types(&self, owner: &str) -> Option<(Vec<&Type>, &Generics)> {
        match self.declarations.get(owner)? {
            Item::Struct(declaration) => Some((
                declaration.fields.iter().map(|field| &field.ty).collect(),
                &declaration.generics,
            )),
            Item::Enum(declaration) => Some((
                declaration
                    .variants
                    .iter()
                    .flat_map(|variant| variant.fields.iter().map(|field| &field.ty))
                    .collect(),
                &declaration.generics,
            )),
            _ => None,
        }
    }

    /// Reports whether a same-file struct or enum derives the standard trait that adds method.
    pub(crate) fn derives_method(&self, owner: &str, method: &str) -> bool {
        let attributes = match self.declarations.get(owner) {
            Some(Item::Struct(declaration)) => &declaration.attrs,
            Some(Item::Enum(declaration)) => &declaration.attrs,
            _ => return false,
        };
        let Some((derived, _)) = DERIVED_TRAIT_METHODS
            .iter()
            .find(|(_, derived_method)| *derived_method == method)
        else {
            return false;
        };
        attributes
            .iter()
            .filter(|attribute| attribute.path().is_ident("derive"))
            .any(|attribute| {
                let mut is_derived = false;
                let parsed = attribute.parse_nested_meta(|meta| {
                    is_derived |= meta
                        .path
                        .segments
                        .last()
                        .is_some_and(|segment| segment.ident == derived);
                    Ok(())
                });
                parsed.is_ok() && is_derived
            })
    }

    pub(crate) fn type_origin(&self, value: &Type, scope: &TypeScope, depth: usize) -> ValueOrigin {
        if depth >= 16 {
            return ValueOrigin::Unknown;
        }
        match value {
            Type::Reference(reference) => self.type_origin(&reference.elem, scope, depth + 1),
            Type::Paren(inner) => self.type_origin(&inner.elem, scope, depth + 1),
            Type::Group(inner) => self.type_origin(&inner.elem, scope, depth + 1),
            Type::Array(array) => {
                ValueOrigin::collection(self.type_origin(&array.elem, scope, depth + 1))
            }
            Type::Slice(slice) => {
                ValueOrigin::collection(self.type_origin(&slice.elem, scope, depth + 1))
            }
            Type::Tuple(tuple) => ValueOrigin::container(
                tuple
                    .elems
                    .iter()
                    .map(|element| self.type_origin(element, scope, depth + 1)),
            ),
            Type::ImplTrait(bounds) if is_conversion_bound(&bounds.bounds) => {
                ValueOrigin::PureStandardLibrary
            }
            Type::TraitObject(_) | Type::ImplTrait(_) => ValueOrigin::External(Certainty::Definite),
            Type::BareFn(_) | Type::Ptr(_) => ValueOrigin::External(Certainty::Possible),
            Type::Path(path) if path.qself.is_none() => self.type_path_origin(path, scope, depth),
            Type::Path(_) => ValueOrigin::External(Certainty::Possible),
            _ => ValueOrigin::Unknown,
        }
    }

    fn type_path_origin(
        &self,
        value: &syn::TypePath,
        scope: &TypeScope,
        depth: usize,
    ) -> ValueOrigin {
        let segments: Vec<String> = value
            .path
            .segments
            .iter()
            .map(|segment| segment.ident.to_string())
            .collect();
        let arguments: Vec<&Type> = value
            .path
            .segments
            .last()
            .map(|segment| match &segment.arguments {
                PathArguments::AngleBracketed(arguments) => arguments
                    .args
                    .iter()
                    .filter_map(|argument| match argument {
                        GenericArgument::Type(argument) => Some(argument),
                        _ => None,
                    })
                    .collect(),
                _ => Vec::new(),
            })
            .unwrap_or_default();
        let last = segments.last().map(String::as_str).unwrap_or_default();
        let first_argument = || {
            arguments
                .first()
                .map_or(ValueOrigin::PureStandardLibrary, |inner| {
                    self.type_origin(inner, scope, depth + 1)
                })
        };
        match self.path_target(&segments, scope) {
            PathTarget::DexSdk | PathTarget::PureStandardLibrary
                if OPTIONAL_TYPES.contains(&last) =>
            {
                ValueOrigin::Optional(Box::new(first_argument()))
            }
            PathTarget::DexSdk => ValueOrigin::DexSdk,
            PathTarget::PureStandardLibrary if TRANSPARENT_WRAPPER_TYPES.contains(&last) => {
                first_argument()
            }
            PathTarget::PureStandardLibrary if COLLECTION_TYPES.contains(&last) => {
                // A map's elements are its values, its last type argument.
                ValueOrigin::collection(
                    arguments
                        .last()
                        .map_or(ValueOrigin::PureStandardLibrary, |element| {
                            self.type_origin(element, scope, depth + 1)
                        }),
                )
            }
            PathTarget::PureStandardLibrary => ValueOrigin::container(
                arguments
                    .iter()
                    .map(|argument| self.type_origin(argument, scope, depth + 1)),
            ),
            PathTarget::SelfType => match segments.get(1) {
                Some(associated) => self
                    .associated_type_of(&scope.owner, associated)
                    .map_or(ValueOrigin::Unknown, |declared| {
                        self.type_origin(declared, scope, depth + 1)
                    }),
                None => ValueOrigin::FileType(scope.owner.clone()),
            },
            PathTarget::FileItem(name) => match self.declarations.get(&name) {
                Some(Item::Type(alias)) => self.type_origin(&alias.ty, scope, depth + 1),
                Some(Item::Struct(_) | Item::Enum(_)) => ValueOrigin::FileType(name),
                _ => ValueOrigin::Unknown,
            },
            PathTarget::DexSdkExternal | PathTarget::ExternalStandardLibrary => {
                ValueOrigin::External(Certainty::Definite)
            }
            PathTarget::External => ValueOrigin::External(Certainty::Possible),
        }
    }

    fn associated_type_of(&self, owner: &str, name: &str) -> Option<&Type> {
        self.implementations
            .iter()
            .filter(|implementation| type_name(&implementation.self_ty).as_deref() == Some(owner))
            .find_map(|implementation| associated_type(implementation, name))
    }
}

/// Reports whether every bound is a standard conversion trait, such as `impl Into<String>`.
fn is_conversion_bound(
    bounds: &syn::punctuated::Punctuated<syn::TypeParamBound, syn::Token![+]>,
) -> bool {
    bounds.iter().all(|bound| match bound {
        syn::TypeParamBound::Trait(bound) => {
            bound.path.segments.last().is_some_and(|segment| {
                CONVERSION_TRAITS.contains(&segment.ident.to_string().as_str())
            })
        }
        syn::TypeParamBound::Lifetime(_) => true,
        _ => false,
    })
}

fn standard_library_target(module_path: &[String]) -> PathTarget {
    let joined = module_path.join("::");
    if BLOCKING_STANDARD_LIBRARY_PATHS
        .iter()
        .any(|blocking| joined == *blocking || joined.starts_with(&format!("{blocking}::")))
    {
        return PathTarget::ExternalStandardLibrary;
    }
    match module_path.first() {
        None => PathTarget::PureStandardLibrary,
        Some(module) if PURE_STANDARD_LIBRARY_MODULES.contains(&module.as_str()) => {
            PathTarget::PureStandardLibrary
        }
        Some(_) => PathTarget::ExternalStandardLibrary,
    }
}

fn dex_sdk_target(segments: &[String]) -> PathTarget {
    if segments
        .iter()
        .any(|segment| DEX_SDK_EXTERNAL_ITEMS.contains(&segment.as_str()))
    {
        PathTarget::DexSdkExternal
    } else {
        PathTarget::DexSdk
    }
}
