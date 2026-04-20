/// Repo Intelligence scanner — stack detection, framework detection,
/// build tool detection, and code convention inference (RI.T02–T04, RI.T07).
use super::profile::{BuildTool, CodeConventions, Framework, PrimaryLanguage, RepoProfile};
use chrono::Utc;
use std::collections::HashMap;
use std::path::Path;

/// Run the full scanner on `repo_path` and return a `RepoProfile`.
///
/// This is a synchronous, blocking operation — call it from a `tokio::task::spawn_blocking`
/// context for large repos.
pub fn scan(repo_path: &Path) -> RepoProfile {
    let primary_lang = detect_primary_language(repo_path);
    let secondary_langs = detect_secondary_languages(repo_path, &primary_lang);
    let frameworks = detect_frameworks(repo_path);
    let build_tools = detect_build_tools(repo_path);
    let monorepo = is_monorepo(repo_path);
    let conventions = infer_conventions(repo_path, &primary_lang);
    let confidence = compute_confidence(repo_path, &primary_lang);

    RepoProfile {
        repo_path: repo_path.to_string_lossy().into_owned(),
        primary_lang,
        secondary_langs,
        frameworks,
        build_tools,
        conventions,
        monorepo,
        confidence,
        scanned_at: Utc::now().to_rfc3339(),
    }
}

// ─── Language detection ───────────────────────────────────────────────────────

/// Extension → language mapping (returns static str key used in counts map).
fn ext_to_language(ext: &str) -> Option<PrimaryLanguage> {
    match ext {
        "rs" => Some(PrimaryLanguage::Rust),
        "ts" | "tsx" | "mts" | "cts" => Some(PrimaryLanguage::TypeScript),
        "js" | "jsx" | "mjs" | "cjs" => Some(PrimaryLanguage::JavaScript),
        "dart" => Some(PrimaryLanguage::Dart),
        "py" | "pyw" => Some(PrimaryLanguage::Python),
        "go" => Some(PrimaryLanguage::Go),
        "rb" | "rake" => Some(PrimaryLanguage::Ruby),
        "swift" => Some(PrimaryLanguage::Swift),
        "kt" | "kts" => Some(PrimaryLanguage::Kotlin),
        "java" => Some(PrimaryLanguage::Java),
        _ => None,
    }
}

/// Detect the primary language of a repository.
///
/// Priority signals (checked first, before file counts):
/// 1. `Cargo.toml` at root → Rust
/// 2. `pubspec.yaml` at root → Dart/Flutter
/// 3. `go.mod` at root → Go
/// 4. `Gemfile` at root → Ruby
/// 5. `Package.swift` at root → Swift
/// 6. `pyproject.toml` / `setup.py` / `requirements.txt` at root → Python
/// 7. `build.gradle` / `build.gradle.kts` / `pom.xml` at root → Java/Kotlin
/// 8. `tsconfig.json` + TS files → TypeScript
/// 9. `package.json` + JS/TS files → JS/TS
/// 10. File extension counts (fallback)
pub fn detect_primary_language(repo_path: &Path) -> PrimaryLanguage {
    // Manifest-first signals (most reliable)
    if repo_path.join("Cargo.toml").exists() {
        return PrimaryLanguage::Rust;
    }
    if repo_path.join("pubspec.yaml").exists() {
        return PrimaryLanguage::Dart;
    }
    if repo_path.join("go.mod").exists() {
        return PrimaryLanguage::Go;
    }
    if repo_path.join("Gemfile").exists() {
        return PrimaryLanguage::Ruby;
    }
    if repo_path.join("Package.swift").exists() {
        return PrimaryLanguage::Swift;
    }
    if repo_path.join("pyproject.toml").exists()
        || repo_path.join("setup.py").exists()
        || repo_path.join("requirements.txt").exists()
    {
        return PrimaryLanguage::Python;
    }
    if repo_path.join("build.gradle.kts").exists() {
        return PrimaryLanguage::Kotlin;
    }
    if repo_path.join("build.gradle").exists() || repo_path.join("pom.xml").exists() {
        return PrimaryLanguage::Java;
    }
    if repo_path.join("tsconfig.json").exists() {
        return PrimaryLanguage::TypeScript;
    }

    // Fall back to file extension counts
    let counts = count_by_language(repo_path, 5);
    let mut ordered: Vec<(PrimaryLanguage, usize)> = counts.into_iter().collect();
    ordered.sort_by_key(|b| std::cmp::Reverse(b.1));

    match ordered.first() {
        Some((lang, count)) if *count > 0 => lang.clone(),
        _ => PrimaryLanguage::Unknown,
    }
}

/// Detect secondary languages (any language with >5 files, excluding primary).
pub fn detect_secondary_languages(
    repo_path: &Path,
    primary: &PrimaryLanguage,
) -> Vec<PrimaryLanguage> {
    let counts = count_by_language(repo_path, 5);
    let mut result: Vec<(PrimaryLanguage, usize)> = counts
        .into_iter()
        .filter(|(lang, count)| lang != primary && *count >= 5)
        .collect();
    result.sort_by_key(|b| std::cmp::Reverse(b.1));
    result.into_iter().map(|(lang, _)| lang).collect()
}

fn count_by_language(repo_path: &Path, max_depth: usize) -> HashMap<PrimaryLanguage, usize> {
    let mut counts: HashMap<PrimaryLanguage, usize> = HashMap::new();
    count_language_recursive(repo_path, &mut counts, 0, max_depth);
    counts
}

fn count_language_recursive(
    dir: &Path,
    counts: &mut HashMap<PrimaryLanguage, usize>,
    depth: usize,
    max_depth: usize,
) {
    if depth > max_depth {
        return;
    }
    let Ok(entries) = std::fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        // Skip hidden dirs, vendor dirs, generated output
        if let Some(name) = path.file_name().and_then(|n| n.to_str()) {
            if should_skip_dir(name) {
                continue;
            }
        }
        if path.is_dir() {
            count_language_recursive(&path, counts, depth + 1, max_depth);
        } else if let Some(ext) = path.extension().and_then(|e| e.to_str()) {
            if let Some(lang) = ext_to_language(ext) {
                *counts.entry(lang).or_insert(0) += 1;
            }
        }
    }
}

fn should_skip_dir(name: &str) -> bool {
    matches!(
        name,
        "." | ".."
            | "node_modules"
            | "target"
            | "vendor"
            | "dist"
            | "build"
            | ".build"
            | ".dart_tool"
            | "__pycache__"
            | ".mypy_cache"
            | ".pytest_cache"
            | ".venv"
            | "venv"
            | ".git"
    )
}

// ─── Confidence ──────────────────────────────────────────────────────────────

fn compute_confidence(repo_path: &Path, primary: &PrimaryLanguage) -> f32 {
    if *primary == PrimaryLanguage::Unknown {
        return 0.0;
    }
    // Manifest present → high confidence
    let has_manifest = match primary {
        PrimaryLanguage::Rust => repo_path.join("Cargo.toml").exists(),
        PrimaryLanguage::Dart => repo_path.join("pubspec.yaml").exists(),
        PrimaryLanguage::Go => repo_path.join("go.mod").exists(),
        PrimaryLanguage::Ruby => repo_path.join("Gemfile").exists(),
        PrimaryLanguage::Swift => repo_path.join("Package.swift").exists(),
        PrimaryLanguage::Python => {
            repo_path.join("pyproject.toml").exists()
                || repo_path.join("setup.py").exists()
                || repo_path.join("requirements.txt").exists()
        }
        PrimaryLanguage::TypeScript => repo_path.join("tsconfig.json").exists(),
        PrimaryLanguage::Kotlin => repo_path.join("build.gradle.kts").exists(),
        PrimaryLanguage::Java => {
            repo_path.join("pom.xml").exists() || repo_path.join("build.gradle").exists()
        }
        _ => false,
    };
    if has_manifest {
        0.95
    } else {
        0.65
    }
}

// ─── Framework detection ─────────────────────────────────────────────────────

/// Detect frameworks and tooling environments (RI.T03).
pub fn detect_frameworks(repo_path: &Path) -> Vec<Framework> {
    let mut detected = Vec::new();

    // Next.js: next.config.js / next.config.mjs / next.config.ts
    if repo_path.join("next.config.js").exists()
        || repo_path.join("next.config.mjs").exists()
        || repo_path.join("next.config.ts").exists()
    {
        detected.push(Framework::NextJs);
    }

    // Vite: vite.config.ts / vite.config.js / vite.config.mts
    if repo_path.join("vite.config.ts").exists()
        || repo_path.join("vite.config.js").exists()
        || repo_path.join("vite.config.mts").exists()
    {
        detected.push(Framework::Vite);
    }

    // Tailwind CSS: tailwind.config.ts / tailwind.config.js / tailwind.config.cjs
    if repo_path.join("tailwind.config.ts").exists()
        || repo_path.join("tailwind.config.js").exists()
        || repo_path.join("tailwind.config.cjs").exists()
    {
        detected.push(Framework::Tailwind);
    }

    // Docker: Dockerfile or docker-compose.yml at root
    if repo_path.join("Dockerfile").exists()
        || repo_path.join("docker-compose.yml").exists()
        || repo_path.join("docker-compose.yaml").exists()
    {
        detected.push(Framework::Docker);
    }

    // GitHub Actions: .github/workflows/ directory exists
    if repo_path.join(".github").join("workflows").exists() {
        detected.push(Framework::GithubActions);
    }

    // Cursor IDE: .cursor/ directory at root
    if repo_path.join(".cursor").exists() {
        detected.push(Framework::Cursor);
    }

    // Claude Code: .claude/ directory at root
    if repo_path.join(".claude").exists() {
        detected.push(Framework::ClaudeCode);
    }

    detected
}

// ─── Build tool detection ─────────────────────────────────────────────────────

/// Detect build orchestration tools (RI.T04).
pub fn detect_build_tools(repo_path: &Path) -> Vec<BuildTool> {
    let mut detected = Vec::new();

    if repo_path.join("Makefile").exists() || repo_path.join("GNUmakefile").exists() {
        detected.push(BuildTool::Make);
    }
    if repo_path.join("justfile").exists() || repo_path.join("Justfile").exists() {
        detected.push(BuildTool::Just);
    }
    if repo_path.join("turbo.json").exists() {
        detected.push(BuildTool::Turbo);
    }
    if repo_path.join("nx.json").exists() {
        detected.push(BuildTool::Nx);
    }
    if repo_path.join("melos.yaml").exists() {
        detected.push(BuildTool::Melos);
    }
    if repo_path.join("Taskfile.yaml").exists() || repo_path.join("Taskfile.yml").exists() {
        detected.push(BuildTool::Taskfile);
    }

    detected
}

// ─── Monorepo detection ──────────────────────────────────────────────────────

fn is_monorepo(repo_path: &Path) -> bool {
    // Common monorepo signals
    repo_path.join("pnpm-workspace.yaml").exists()
        || repo_path.join("lerna.json").exists()
        || repo_path.join("nx.json").exists()
        || repo_path.join("turbo.json").exists()
        || repo_path.join("melos.yaml").exists()
        || repo_path.join("Cargo.toml").exists() && {
            // Cargo workspace: [workspace] section in Cargo.toml
            std::fs::read_to_string(repo_path.join("Cargo.toml"))
                .map(|s| s.contains("[workspace]"))
                .unwrap_or(false)
        }
}

// ─── Convention inference ─────────────────────────────────────────────────────

/// Infer code style conventions by scanning up to 20 source files (RI.T07).
pub fn infer_conventions(repo_path: &Path, primary: &PrimaryLanguage) -> CodeConventions {
    let ext = match primary {
        PrimaryLanguage::Rust => "rs",
        PrimaryLanguage::TypeScript => "ts",
        PrimaryLanguage::JavaScript => "js",
        PrimaryLanguage::Dart => "dart",
        PrimaryLanguage::Python => "py",
        PrimaryLanguage::Go => "go",
        PrimaryLanguage::Ruby => "rb",
        PrimaryLanguage::Swift => "swift",
        PrimaryLanguage::Kotlin => "kt",
        PrimaryLanguage::Java => "java",
        PrimaryLanguage::Unknown => return CodeConventions::default(),
    };

    let files = collect_source_files(repo_path, ext, 20);
    if files.is_empty() {
        return CodeConventions::default();
    }

    let mut tab_count = 0usize;
    let mut two_space_count = 0usize;
    let mut four_space_count = 0usize;
    let mut max_line = 0usize;

    for file in &files {
        let Ok(content) = std::fs::read_to_string(file) else {
            continue;
        };
        for line in content.lines() {
            let len = line.len();
            if len > max_line {
                max_line = len;
            }
            if line.starts_with('\t') {
                tab_count += 1;
            } else if line.starts_with("    ") {
                four_space_count += 1;
            } else if line.starts_with("  ") && !line.starts_with("   ") {
                two_space_count += 1;
            }
        }
    }

    let indentation = if tab_count > two_space_count && tab_count > four_space_count {
        Some("tabs".to_string())
    } else if two_space_count >= four_space_count {
        Some("2-space".to_string())
    } else {
        Some("4-space".to_string())
    };

    // Clamp max_line_length: ignore files with very long auto-generated lines
    let max_line_length = if max_line > 500 { None } else { Some(max_line) };

    // Naming style: use language defaults (convention inference heuristics would
    // require parsing ASTs; for now we return the canonical style for the language)
    let naming_style = language_naming_style(primary);

    CodeConventions {
        naming_style,
        indentation,
        max_line_length,
    }
}

fn language_naming_style(lang: &PrimaryLanguage) -> Option<String> {
    match lang {
        PrimaryLanguage::Rust | PrimaryLanguage::Python | PrimaryLanguage::Go => {
            Some("snake_case".to_string())
        }
        PrimaryLanguage::TypeScript
        | PrimaryLanguage::JavaScript
        | PrimaryLanguage::Dart
        | PrimaryLanguage::Swift
        | PrimaryLanguage::Kotlin
        | PrimaryLanguage::Java => Some("camelCase".to_string()),
        PrimaryLanguage::Ruby => Some("snake_case".to_string()),
        _ => None,
    }
}

fn collect_source_files(repo_path: &Path, ext: &str, limit: usize) -> Vec<std::path::PathBuf> {
    let mut files = Vec::new();
    collect_source_recursive(repo_path, ext, &mut files, 0, 4, limit);
    files
}

fn collect_source_recursive(
    dir: &Path,
    ext: &str,
    files: &mut Vec<std::path::PathBuf>,
    depth: usize,
    max_depth: usize,
    limit: usize,
) {
    if depth > max_depth || files.len() >= limit {
        return;
    }
    let Ok(entries) = std::fs::read_dir(dir) else {
        return;
    };
    // Collect and sort entries for deterministic ordering
    let mut paths: Vec<_> = entries.flatten().map(|e| e.path()).collect();
    paths.sort();
    for path in paths {
        if files.len() >= limit {
            return;
        }
        if let Some(name) = path.file_name().and_then(|n| n.to_str()) {
            if should_skip_dir(name) {
                continue;
            }
        }
        if path.is_dir() {
            collect_source_recursive(&path, ext, files, depth + 1, max_depth, limit);
        } else if path.extension().and_then(|e| e.to_str()) == Some(ext) {
            files.push(path);
        }
    }
}

// ─── Tests ───────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn detects_rust_from_cargo_toml() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("Cargo.toml"), b"[package]\n").unwrap();
        assert_eq!(detect_primary_language(tmp.path()), PrimaryLanguage::Rust);
    }

    #[test]
    fn detects_dart_from_pubspec() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("pubspec.yaml"), b"name: myapp\n").unwrap();
        assert_eq!(detect_primary_language(tmp.path()), PrimaryLanguage::Dart);
    }

    #[test]
    fn detects_typescript_from_tsconfig() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("tsconfig.json"), b"{}").unwrap();
        assert_eq!(
            detect_primary_language(tmp.path()),
            PrimaryLanguage::TypeScript
        );
    }

    #[test]
    fn detects_go_from_go_mod() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("go.mod"), b"module example.com/app\n").unwrap();
        assert_eq!(detect_primary_language(tmp.path()), PrimaryLanguage::Go);
    }

    #[test]
    fn detects_python_from_pyproject() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("pyproject.toml"), b"[tool.poetry]\n").unwrap();
        assert_eq!(detect_primary_language(tmp.path()), PrimaryLanguage::Python);
    }

    #[test]
    fn detects_ruby_from_gemfile() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(
            tmp.path().join("Gemfile"),
            b"source 'https://rubygems.org'\n",
        )
        .unwrap();
        assert_eq!(detect_primary_language(tmp.path()), PrimaryLanguage::Ruby);
    }

    #[test]
    fn unknown_for_empty_dir() {
        let tmp = TempDir::new().unwrap();
        assert_eq!(
            detect_primary_language(tmp.path()),
            PrimaryLanguage::Unknown
        );
    }

    #[test]
    fn detects_github_actions_framework() {
        let tmp = TempDir::new().unwrap();
        std::fs::create_dir_all(tmp.path().join(".github").join("workflows")).unwrap();
        let frameworks = detect_frameworks(tmp.path());
        assert!(frameworks.contains(&Framework::GithubActions));
    }

    #[test]
    fn detects_docker_framework() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("Dockerfile"), b"FROM ubuntu\n").unwrap();
        let frameworks = detect_frameworks(tmp.path());
        assert!(frameworks.contains(&Framework::Docker));
    }

    #[test]
    fn detects_make_build_tool() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("Makefile"), b"all:\n\techo ok\n").unwrap();
        let tools = detect_build_tools(tmp.path());
        assert!(tools.contains(&BuildTool::Make));
    }

    #[test]
    fn detects_turbo_and_marks_monorepo() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("turbo.json"), b"{}").unwrap();
        let tools = detect_build_tools(tmp.path());
        assert!(tools.contains(&BuildTool::Turbo));
        assert!(is_monorepo(tmp.path()));
    }

    #[test]
    fn infer_indentation_tabs() {
        let tmp = TempDir::new().unwrap();
        let content = "\tlet x = 1;\n\tlet y = 2;\n\tprintln!(\"{}\", x + y);\n";
        std::fs::write(tmp.path().join("main.rs"), content.as_bytes()).unwrap();
        let conv = infer_conventions(tmp.path(), &PrimaryLanguage::Rust);
        assert_eq!(conv.indentation.as_deref(), Some("tabs"));
    }

    #[test]
    fn scan_returns_profile() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("Cargo.toml"), b"[package]\n").unwrap();
        let profile = scan(tmp.path());
        assert_eq!(profile.primary_lang, PrimaryLanguage::Rust);
        assert!(profile.confidence > 0.9);
    }
}
