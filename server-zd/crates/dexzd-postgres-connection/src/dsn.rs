// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/// The libpq TLS parameters that tokio-postgres rejects, removed from the connection string.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct TlsParameters {
    pub(crate) ssl_mode: Option<String>,
    pub(crate) ssl_root_cert: Option<String>,
    pub(crate) ssl_sni: Option<String>,
    /// Read but kept in the connection string, because tokio-postgres implements it.
    pub(crate) ssl_negotiation: Option<String>,
}

/// One parameter of a connection string and its raw text span.
struct Parameter {
    key: String,
    value: String,
    span: (usize, usize),
    /// The value as written: still escaped or percent-encoded, without quotes.
    raw_value_span: (usize, usize),
}

/// Removes the TLS parameters, keeping the rest verbatim; errors never quote the credential-bearing DSN.
pub(crate) fn split_tls_parameters(dsn: &str) -> Result<(String, TlsParameters), String> {
    let (prefix, parameters, separator) = match url_query_start(dsn) {
        Some(query_start) => (&dsn[..query_start], url_parameters(dsn, query_start)?, "&"),
        None if is_url(dsn) => (dsn, Vec::new(), "&"),
        None => ("", key_value_parameters(dsn)?, " "),
    };
    let mut tls = TlsParameters::default();
    let mut kept = Vec::new();
    for parameter in parameters {
        let raw = &dsn[parameter.span.0..parameter.span.1];
        match parameter.key.as_str() {
            "sslmode" => tls.ssl_mode = Some(parameter.value),
            "sslrootcert" => tls.ssl_root_cert = Some(parameter.value),
            "sslsni" => tls.ssl_sni = Some(parameter.value),
            "sslnegotiation" => {
                tls.ssl_negotiation = Some(parameter.value);
                kept.push(raw);
            }
            _ => kept.push(raw),
        }
    }
    let remaining = match (is_url(dsn), kept.is_empty()) {
        (true, true) => prefix.trim_end_matches('?').to_owned(),
        (true, false) => format!("{}?{}", prefix.trim_end_matches('?'), kept.join(separator)),
        (false, _) => kept.join(separator),
    };
    Ok((remaining, tls))
}

/// Returns each nonempty password as written, plus a URL userinfo carrying one; errors never quote the DSN.
pub(crate) fn written_password_spellings(dsn: &str) -> Result<Vec<String>, String> {
    let mut spellings = dsn_parameters(dsn)?
        .into_iter()
        .filter(|parameter| parameter.key == "password")
        .map(|parameter| dsn[parameter.raw_value_span.0..parameter.raw_value_span.1].to_owned())
        .filter(|password| !password.is_empty())
        .collect::<Vec<_>>();
    if let Some(userinfo) = url_userinfo(dsn)
        && let Some((_, password)) = userinfo.split_once(':')
        && !password.is_empty()
    {
        spellings.push(password.to_owned());
        spellings.push(userinfo.to_owned());
    }
    Ok(spellings)
}

/// Parses the URL query or keyword parameters like tokio-postgres.
fn dsn_parameters(dsn: &str) -> Result<Vec<Parameter>, String> {
    match url_query_start(dsn) {
        Some(query_start) => url_parameters(dsn, query_start),
        None if is_url(dsn) => Ok(Vec::new()),
        None => key_value_parameters(dsn),
    }
}

/// Returns the text before the first `@`, where tokio-postgres reads the user and password.
fn url_userinfo(dsn: &str) -> Option<&str> {
    if !is_url(dsn) {
        return None;
    }
    let authority_start = dsn.find("://")? + 3;
    dsn[authority_start..]
        .find('@')
        .map(|at| &dsn[authority_start..authority_start + at])
}

fn is_url(dsn: &str) -> bool {
    dsn.starts_with("postgres://") || dsn.starts_with("postgresql://")
}

/// Returns the index just after `?`, locating it like tokio-postgres: after the first `@`, if any.
fn url_query_start(dsn: &str) -> Option<usize> {
    if !is_url(dsn) {
        return None;
    }
    let authority_start = dsn.find("://")? + 3;
    let host_start = dsn[authority_start..]
        .find('@')
        .map_or(authority_start, |at| authority_start + at + 1);
    dsn[host_start..]
        .find('?')
        .map(|question| host_start + question + 1)
}

/// Parses `key=value&...` after the query start like tokio-postgres, percent-decoding both parts.
fn url_parameters(dsn: &str, query_start: usize) -> Result<Vec<Parameter>, String> {
    let mut parameters = Vec::new();
    let mut position = query_start;
    while position < dsn.len() {
        let Some(equals) = dsn[position..].find('=').map(|offset| position + offset) else {
            return Err("connection URL has an unterminated parameter".to_owned());
        };
        let value_end = dsn[equals + 1..]
            .find('&')
            .map_or(dsn.len(), |offset| equals + 1 + offset);
        parameters.push(Parameter {
            key: percent_decode(&dsn[position..equals])?,
            value: percent_decode(&dsn[equals + 1..value_end])?,
            span: (position, value_end),
            raw_value_span: (equals + 1, value_end),
        });
        position = value_end + 1;
    }
    Ok(parameters)
}

/// Lenient percent-decoding, like the percent-encoding crate tokio-postgres uses.
fn percent_decode(text: &str) -> Result<String, String> {
    let bytes = text.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        let escape = bytes
            .get(index + 1..index + 3)
            .filter(|_| bytes[index] == b'%')
            .and_then(|digits| std::str::from_utf8(digits).ok())
            .and_then(|digits| u8::from_str_radix(digits, 16).ok());
        match escape {
            Some(byte) => {
                decoded.push(byte);
                index += 3;
            }
            None => {
                decoded.push(bytes[index]);
                index += 1;
            }
        }
    }
    String::from_utf8(decoded).map_err(|_| "connection URL parameter is not UTF-8".to_owned())
}

/// Parses `key = value` pairs like tokio-postgres: values are bare with backslash escapes, or single-quoted.
fn key_value_parameters(dsn: &str) -> Result<Vec<Parameter>, String> {
    let characters = dsn.char_indices().collect::<Vec<_>>();
    let offset_at = |index: usize| {
        characters
            .get(index)
            .map_or(dsn.len(), |(offset, _)| *offset)
    };
    let mut parameters = Vec::new();
    let mut index = 0;
    loop {
        while characters
            .get(index)
            .is_some_and(|(_, character)| character.is_whitespace())
        {
            index += 1;
        }
        let start = index;
        while characters
            .get(index)
            .is_some_and(|(_, character)| !character.is_whitespace() && *character != '=')
        {
            index += 1;
        }
        if index == start {
            if index < characters.len() {
                return Err("connection string has a parameter without a keyword".to_owned());
            }
            return Ok(parameters);
        }
        let key = dsn[offset_at(start)..offset_at(index)].to_owned();
        while characters
            .get(index)
            .is_some_and(|(_, character)| character.is_whitespace())
        {
            index += 1;
        }
        if characters.get(index).map(|(_, character)| *character) != Some('=') {
            return Err("connection string has a keyword without '='".to_owned());
        }
        index += 1;
        while characters
            .get(index)
            .is_some_and(|(_, character)| character.is_whitespace())
        {
            index += 1;
        }
        let is_quoted = characters.get(index).map(|(_, character)| *character) == Some('\'');
        if is_quoted {
            index += 1;
        }
        let raw_value_start = offset_at(index);
        let mut value = String::new();
        loop {
            let Some(&(_, character)) = characters.get(index) else {
                if is_quoted {
                    return Err("connection string has an unterminated quoted value".to_owned());
                }
                break;
            };
            if (is_quoted && character == '\'') || (!is_quoted && character.is_whitespace()) {
                break;
            }
            if character == '\\' {
                index += 1;
                let Some(&(_, escaped)) = characters.get(index) else {
                    return Err("connection string ends with an unfinished escape".to_owned());
                };
                value.push(escaped);
            } else {
                value.push(character);
            }
            index += 1;
        }
        let raw_value_end = offset_at(index);
        if is_quoted {
            index += 1;
        } else if value.is_empty() {
            return Err("connection string has a keyword without a value".to_owned());
        }
        parameters.push(Parameter {
            key,
            value,
            span: (offset_at(start), offset_at(index)),
            raw_value_span: (raw_value_start, raw_value_end),
        });
    }
}
