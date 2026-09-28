//! URL, domain and path helpers (ports of the matching functions in
//! `helper/helpers.go`).

use std::net::{IpAddr, UdpSocket};
use std::path::PathBuf;
use std::sync::LazyLock;

use http::HeaderMap;
use regex::Regex;

static RE_HTTP_SCHEME: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"http(s?)://").unwrap());
static RE_MAIN_DOMAIN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"((.*)[.])?([a-zA-Z0-9_-]{3,}[.]([a-z]{2,3}([.][a-z]{2,})?))").unwrap()
});

/// Returns the `host[:port]` part of a URL, adding an `https://` scheme first
/// when there is none. `https://api.example.com:8080/x` -> `api.example.com:8080`.
pub fn extract_domain(input: &str) -> String {
    let rest = input
        .strip_prefix("http://")
        .or_else(|| input.strip_prefix("https://"))
        .unwrap_or(input);

    let authority = rest.split(['/', '?', '#']).next().unwrap_or_default();

    let host = match authority.rsplit_once('@') {
        Some((_, host)) => host,
        None => authority,
    };

    if host.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return String::new();
    }

    host.to_string()
}

/// The registrable domain of a host: `a.b.example.co.tz` -> `example.co.tz`.
pub fn get_main_domain(value: &str) -> Option<String> {
    let caps = RE_MAIN_DOMAIN.captures(value)?;
    let domain = caps.get(3)?.as_str();

    (!domain.is_empty()).then(|| domain.to_string())
}

/// Builds an absolute `https://` URL for `path` on `domain`, keeping any
/// configured base URL prefix. Absolute URLs are returned unchanged.
pub fn get_base_url(path: &str, domain: &str, base_url: &str, port: u16) -> String {
    if RE_HTTP_SCHEME.is_match(path) {
        return path.to_string();
    }

    let domain = if domain.is_empty() {
        format!(
            "{}:{}",
            local_ip().unwrap_or_else(|| "127.0.0.1".into()),
            port
        )
    } else {
        domain.to_string()
    };

    format!(
        "https://{}{}/{}",
        domain,
        base_url.trim_end_matches('/'),
        path.trim_end_matches('/').trim_start_matches('/')
    )
}

/// Client IP from `X-Forwarded-For` / `X-Real-Ip`, falling back to the
/// connection's peer address. The Go version returns an empty string when
/// neither header is set; the peer address fallback is the only difference.
pub fn get_client_ip(headers: &HeaderMap, peer: Option<IpAddr>) -> String {
    let header = |name: &str| {
        headers
            .get(name)
            .and_then(|v| v.to_str().ok())
            .unwrap_or_default()
            .to_string()
    };

    let mut ip = header("x-forwarded-for");
    if ip.is_empty() {
        ip = header("x-real-ip");
    }

    if !ip.is_empty() {
        return ip.split(':').next().unwrap_or_default().to_string();
    }

    peer.map(|p| p.to_string()).unwrap_or_default()
}

/// First non-loopback IPv4 address of this machine, found by asking the OS
/// which interface would route to a public address (nothing is sent).
pub fn local_ip() -> Option<String> {
    let socket = UdpSocket::bind("0.0.0.0:0").ok()?;
    socket.connect("8.8.8.8:80").ok()?;
    let ip = socket.local_addr().ok()?.ip();

    (!ip.is_loopback() && !ip.is_unspecified()).then(|| ip.to_string())
}

/// `~/.yekonga-server/<name>`, created if missing. Holds the local database
/// files and uploads.
pub fn home_directory(name: &str) -> PathBuf {
    let home = std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
        .filter(|p| !p.as_os_str().is_empty() && p.as_os_str() != "/")
        .unwrap_or_else(|| PathBuf::from("/root"));

    let dir = home.join(".yekonga-server").join(name);
    if let Err(err) = std::fs::create_dir_all(&dir) {
        tracing::error!(dir = %dir.display(), %err, "cannot create home directory");
    }

    dir
}

/// Shell-style pattern match with Go's `path.Match` semantics: `*` matches any
/// run of non-`/` characters, `?` one non-`/` character, `[...]` a character
/// class (`^` negates) and `\` escapes. A malformed pattern never matches.
pub fn match_path(route: &str, pattern: &str) -> bool {
    let name: Vec<char> = route.chars().collect();
    let pattern: Vec<char> = pattern.chars().collect();

    glob_match(&pattern, &name).unwrap_or(false)
}

fn glob_match(pattern: &[char], name: &[char]) -> Option<bool> {
    let (mut p, mut n) = (0, 0);
    // Backtracking point for the last `*`: (pattern index after it, name index).
    let mut star: Option<(usize, usize)> = None;

    loop {
        if p < pattern.len() {
            match pattern[p] {
                '*' => {
                    star = Some((p + 1, n));
                    p += 1;
                    continue;
                }
                c if n < name.len() => {
                    let consumed = match c {
                        '?' => (name[n] != '/').then_some(1),
                        '[' => {
                            let (matched, len) = match_class(&pattern[p..], name[n])?;
                            matched.then_some(len)
                        }
                        '\\' => {
                            let escaped = *pattern.get(p + 1)?;
                            (escaped == name[n]).then_some(2)
                        }
                        c => (c == name[n]).then_some(1),
                    };

                    if let Some(len) = consumed {
                        p += len;
                        n += 1;
                        continue;
                    }
                }
                _ => {}
            }
        } else if n == name.len() {
            return Some(true);
        }

        // Mismatch: let the last `*` swallow one more character, unless that
        // character is a separator (a `*` never crosses `/`).
        match star {
            Some((sp, sn)) if sn < name.len() && name[sn] != '/' => {
                star = Some((sp, sn + 1));
                p = sp;
                n = sn + 1;
            }
            _ => {
                // Go still reports a malformed rest of the pattern as an error.
                validate_pattern(&pattern[p.min(pattern.len())..])?;
                return Some(false);
            }
        }
    }
}

/// Matches `c` against the class starting at `pattern[0] == '['`. Returns
/// whether it matched and the class's length, or `None` if malformed.
fn match_class(pattern: &[char], c: char) -> Option<(bool, usize)> {
    let mut i = 1;
    let negated = pattern.get(i) == Some(&'^');
    if negated {
        i += 1;
    }

    let mut matched = false;
    let mut first = true;

    loop {
        let ch = *pattern.get(i)?;
        if ch == ']' && !first {
            i += 1;
            break;
        }
        first = false;

        let (lo, len) = class_char(&pattern[i..])?;
        i += len;

        let hi = if pattern.get(i) == Some(&'-') {
            let (hi, len) = class_char(&pattern[i + 1..])?;
            i += 1 + len;
            hi
        } else {
            lo
        };

        if lo <= c && c <= hi {
            matched = true;
        }
    }

    Some((matched != negated, i))
}

fn class_char(pattern: &[char]) -> Option<(char, usize)> {
    match *pattern.first()? {
        '-' | ']' => None,
        '\\' => Some((*pattern.get(1)?, 2)),
        c => Some((c, 1)),
    }
}

fn validate_pattern(pattern: &[char]) -> Option<()> {
    let mut i = 0;
    while i < pattern.len() {
        match pattern[i] {
            '\\' => {
                pattern.get(i + 1)?;
                i += 2;
            }
            '[' => {
                let (_, len) = match_class(&pattern[i..], '\0')?;
                i += len;
            }
            _ => i += 1,
        }
    }

    Some(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn domains() {
        assert_eq!(
            extract_domain("https://api.example.com:8080/x?y"),
            "api.example.com:8080"
        );
        assert_eq!(extract_domain("localhost:8080"), "localhost:8080");
        assert_eq!(extract_domain("http://user:pw@host.tz/path"), "host.tz");
        assert_eq!(extract_domain(""), "");

        assert_eq!(
            get_main_domain("shop.example.co.tz").as_deref(),
            Some("example.co.tz")
        );
        assert_eq!(
            get_main_domain("a.b.example.com").as_deref(),
            Some("example.com")
        );
        assert_eq!(get_main_domain("localhost"), None);
    }

    #[test]
    fn base_url() {
        assert_eq!(
            get_base_url("/logout", "app.tz", "", 80),
            "https://app.tz/logout"
        );
        assert_eq!(
            get_base_url("refresh", "app.tz", "/v1/", 80),
            "https://app.tz/v1/refresh"
        );
        assert_eq!(
            get_base_url("https://x.tz/a", "app.tz", "", 80),
            "https://x.tz/a"
        );
    }

    #[test]
    fn path_match() {
        assert!(match_path("/me/admin", "/me/*"));
        assert!(!match_path("/me/admin/x", "/me/*"));
        assert!(match_path("/me/", "/me/*"));
        assert!(!match_path("/me", "/me/*"));
        assert!(match_path("/image/1", "/image/?"));
        assert!(match_path("/a/b.css", "/a/*.css"));
        assert!(match_path("/x/c", "/x/[a-c]"));
        assert!(!match_path("/x/d", "/x/[a-c]"));
        assert!(match_path("/x/d", "/x/[^a-c]"));
        assert!(match_path("/api/login", "/api/login"));
        assert!(!match_path("/x", "/[x"));
        assert!(match_path("*", "\\*"));
    }
}
