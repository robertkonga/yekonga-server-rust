//! Phone, email and random-code helpers (ports of `FormatPhone`, `IsPhone`,
//! `IsEmail`, `GetRandomString` and `HashRefreshToken` in
//! `helper/helpers.go`).

use std::sync::LazyLock;

use rand::Rng;
use regex::Regex;
use sha2::{Digest, Sha256};

static RE_EMAIL: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r#"^(([^<>()\[\]\\.,;:\s@"]+(\.[^<>()\[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$"#,
    )
    .unwrap()
});
static RE_PHONE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?im)^[\+]?[(]?[0-9]{3}[)]?[-\s\.]?[0-9]{3}[-\s\.]?[0-9]{4,6}$").unwrap()
});

/// Normalizes a phone number to digits with the country code, Tanzanian
/// numbers by default: `0712 345 678` -> `255712345678`, `+1…` -> `1…`.
/// Returns `""` when it can't be a phone number.
pub fn format_phone(phone: &str) -> String {
    let mut value: String = phone
        .chars()
        .filter(|c| !matches!(c, ' ' | '-' | '_' | '.' | ','))
        .collect();

    if let Some(rest) = value.strip_prefix('+') {
        value = rest.to_string();
    } else if value.starts_with("255") {
    } else if value.starts_with('0') && value.len() == 10 {
        value = format!("255{}", &value[1..]);
    } else if !value.starts_with('0') && value.len() == 9 {
        value = format!("255{value}");
    }

    if value.len() < 10 || (value.starts_with("255") && value.len() != 12) {
        return String::new();
    }
    value
}

pub fn is_phone(value: &str) -> bool {
    !value.is_empty() && RE_PHONE.is_match(&format_phone(value))
}

pub fn is_email(value: &str) -> bool {
    RE_EMAIL.is_match(&value.to_lowercase())
}

/// A random string from the operating system's generator. `mode` is
/// `"number"` (digits, not starting with 0), `"letter"`, `"hex"` or anything
/// else for digits and capital letters.
///
/// Go builds these from a `math/rand` source seeded with the clock, so its
/// OTP codes and refresh tokens can be predicted.
pub fn random_string(length: usize, mode: &str) -> String {
    const DIGITS: &str = "0123456789";
    const LETTERS: &str = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
    let chars: Vec<u8> = match mode {
        "number" => DIGITS.into(),
        "letter" => LETTERS.into(),
        "hex" => format!("{DIGITS}ABCDEFabcdef").into(),
        _ => format!("{DIGITS}{LETTERS}").into(),
    };

    let mut rng = rand::rng();
    (0..length)
        .map(|i| {
            let from = usize::from(mode == "number" && i == 0);
            chars[rng.random_range(from..chars.len())] as char
        })
        .collect()
}

/// A random number of `length` digits, as a string (Go's `GetRandomInt`).
pub fn random_digits(length: usize) -> String {
    random_string(length, "number")
}

/// The stored form of a refresh token: its SHA-256, in hex.
pub fn hash_refresh_token(token: &str) -> String {
    Sha256::digest(token.as_bytes())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn random_codes() {
        let code = random_digits(4);
        assert_eq!(code.len(), 4);
        assert!(code.chars().all(|c| c.is_ascii_digit()));
        assert_ne!(code.as_bytes()[0], b'0');

        let token = random_string(64, "");
        assert_eq!(token.len(), 64);
        assert!(token
            .chars()
            .all(|c| c.is_ascii_digit() || c.is_ascii_uppercase()));
        assert_ne!(token, random_string(64, ""));
    }

    #[test]
    fn refresh_token_hash() {
        assert_eq!(
            hash_refresh_token("abc"),
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        );
    }
}
