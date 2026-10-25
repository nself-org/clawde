// SPDX-License-Identifier: MIT
//! Workflow recipes — AE.T19–T20 (Autonomous Execution Engine, Sprint J).
//!
//! A `WorkflowRecipe` is a named sequence of session-mode steps.  Each step
//! transitions the session to a given mode (FORGE/CRUNCH/LEARN/STORM) and
//! runs until a specified condition is met.
//!
//! Recipes are stored as YAML files under `.clawd/recipes/{name}.yaml` in the
//! repo root (or under the user's config dir).  The `RecipeEngine` is
//! responsible for loading, matching, and executing recipe steps.
//!
//! ## File format (YAML)
//!
//! ```yaml
//! name: plan-then-execute
//! trigger_pattern: "^/run "
//! steps:
//!   - action: set_mode
//!     params:
//!       mode: FORGE
//!       until: plan_approved
//!   - action: set_mode
//!     params:
//!       mode: CRUNCH
//!       until: all_tasks_done
//! ```

use anyhow::{bail, Result};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::Path;

// ─── Recipe types ─────────────────────────────────────────────────────────────

/// A single step in a workflow recipe.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RecipeStep {
    /// The action to perform (e.g. `"set_mode"`, `"send_message"`).
    pub action: String,
    /// Free-form parameters for this step.
    #[serde(default)]
    pub params: HashMap<String, String>,
}

/// A complete named workflow recipe.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkflowRecipe {
    /// Unique recipe name (slug).
    pub id: String,
    /// Human-readable display name.
    pub name: String,
    /// Regex or prefix pattern that triggers this recipe automatically.
    /// Empty string = only manually invoked via `recipe.run`.
    #[serde(default)]
    pub trigger_pattern: String,
    /// Ordered list of steps.
    pub steps: Vec<RecipeStep>,
}

impl WorkflowRecipe {
    /// Build a basic FORGE → CRUNCH recipe for quick scaffolding.
    pub fn forge_then_crunch(name: &str) -> Self {
        Self {
            id: slug(name),
            name: name.to_owned(),
            trigger_pattern: String::new(),
            steps: vec![
                RecipeStep {
                    action: "set_mode".to_owned(),
                    params: [
                        ("mode".to_owned(), "FORGE".to_owned()),
                        ("until".to_owned(), "plan_approved".to_owned()),
                    ]
                    .into(),
                },
                RecipeStep {
                    action: "set_mode".to_owned(),
                    params: [
                        ("mode".to_owned(), "CRUNCH".to_owned()),
                        ("until".to_owned(), "all_tasks_done".to_owned()),
                    ]
                    .into(),
                },
            ],
        }
    }
}

// ─── RecipeEngine ─────────────────────────────────────────────────────────────

/// Manages the in-memory recipe registry and trigger matching.
pub struct RecipeEngine {
    recipes: Vec<WorkflowRecipe>,
}

impl RecipeEngine {
    pub fn new() -> Self {
        Self {
            recipes: Vec::new(),
        }
    }

    /// Register a recipe.
    pub fn register(&mut self, recipe: WorkflowRecipe) {
        // Remove any existing recipe with the same id before inserting.
        self.recipes.retain(|r| r.id != recipe.id);
        self.recipes.push(recipe);
    }

    /// Return an immutable view of all registered recipes.
    pub fn list(&self) -> &[WorkflowRecipe] {
        &self.recipes
    }

    /// Find a recipe whose `trigger_pattern` matches `message`.
    ///
    /// Uses prefix matching when the pattern has no regex metacharacters,
    /// and falls back to `str::contains` otherwise.  Returns `None` when
    /// no recipe matches or when the pattern is empty.
    pub fn match_recipe<'a>(
        &'a self,
        message: &str,
        recipes: &'a [WorkflowRecipe],
    ) -> Option<&'a WorkflowRecipe> {
        let search = if recipes.is_empty() {
            self.recipes.as_slice()
        } else {
            recipes
        };

        search.iter().find(|r| {
            !r.trigger_pattern.is_empty() && message_matches_pattern(message, &r.trigger_pattern)
        })
    }

    /// Load recipes from YAML files in `recipes_dir`.
    ///
    /// Any file ending in `.yaml` or `.yml` inside the directory is
    /// treated as a recipe definition.
    pub fn load_from_dir(&mut self, recipes_dir: &Path) -> Result<usize> {
        if !recipes_dir.exists() {
            return Ok(0);
        }

        let mut loaded = 0;
        let entries = std::fs::read_dir(recipes_dir)?;
        for entry in entries.flatten() {
            let path = entry.path();
            let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
            if ext != "yaml" && ext != "yml" {
                continue;
            }

            match load_recipe_file(&path) {
                Ok(recipe) => {
                    self.register(recipe);
                    loaded += 1;
                }
                Err(e) => {
                    tracing::warn!(path = %path.display(), err = %e, "failed to parse recipe");
                }
            }
        }
        Ok(loaded)
    }
}

impl Default for RecipeEngine {
    fn default() -> Self {
        Self::new()
    }
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/// Parse a single YAML recipe file.
fn load_recipe_file(path: &Path) -> Result<WorkflowRecipe> {
    let content = std::fs::read_to_string(path)?;
    let raw: serde_json::Value = serde_norway::from_str(&content)
        .map_err(|e| anyhow::anyhow!("YAML parse error in {}: {}", path.display(), e))?;

    // Assign id from filename if not present in YAML.
    let stem = path
        .file_stem()
        .and_then(|s| s.to_str())
        .unwrap_or("recipe");

    let name = raw["name"].as_str().unwrap_or(stem).to_owned();

    let trigger_pattern = raw["trigger_pattern"].as_str().unwrap_or("").to_owned();

    let id = raw["id"].as_str().unwrap_or(&slug(&name)).to_owned();

    let steps: Vec<RecipeStep> = if let Some(arr) = raw["steps"].as_array() {
        arr.iter()
            .filter_map(|step| {
                let action = step["action"].as_str()?.to_owned();
                let params: HashMap<String, String> = step["params"]
                    .as_object()
                    .map(|obj| {
                        obj.iter()
                            .filter_map(|(k, v)| Some((k.clone(), v.as_str()?.to_owned())))
                            .collect()
                    })
                    .unwrap_or_default();
                Some(RecipeStep { action, params })
            })
            .collect()
    } else {
        bail!("recipe must have a 'steps' array");
    };

    Ok(WorkflowRecipe {
        id,
        name,
        trigger_pattern,
        steps,
    })
}

/// Simple pattern matching: prefix or contains.
fn message_matches_pattern(message: &str, pattern: &str) -> bool {
    // Regex-like anchors
    if let Some(p) = pattern.strip_prefix('^') {
        return message.starts_with(p);
    }
    if let Some(p) = pattern.strip_suffix('$') {
        return message.ends_with(p);
    }
    message.contains(pattern)
}

/// Convert a recipe name to a URL-safe slug.
fn slug(name: &str) -> String {
    name.to_ascii_lowercase()
        .chars()
        .map(|c| {
            if c.is_alphanumeric() || c == '-' {
                c
            } else {
                '-'
            }
        })
        .collect::<String>()
        .split('-')
        .filter(|s| !s.is_empty())
        .collect::<Vec<_>>()
        .join("-")
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn forge_crunch() -> WorkflowRecipe {
        WorkflowRecipe::forge_then_crunch("Plan then execute")
    }

    #[test]
    fn test_register_and_list() {
        let mut engine = RecipeEngine::new();
        engine.register(forge_crunch());
        assert_eq!(engine.list().len(), 1);
    }

    #[test]
    fn test_register_replaces_duplicate_id() {
        let mut engine = RecipeEngine::new();
        engine.register(forge_crunch());
        engine.register(forge_crunch());
        assert_eq!(engine.list().len(), 1);
    }

    #[test]
    fn test_match_recipe_prefix() {
        let recipe = WorkflowRecipe {
            id: "run-plan".to_owned(),
            name: "Run Plan".to_owned(),
            trigger_pattern: "^/run ".to_owned(),
            steps: vec![],
        };
        let engine = RecipeEngine::new();
        let recipes_clone = [recipe.clone()];
        let matched = engine.match_recipe("/run my-feature", &recipes_clone);
        assert!(matched.is_some());
        let recipes_final = [recipe];
        let no_match = engine.match_recipe("build something", &recipes_final);
        assert!(no_match.is_none());
    }

    #[test]
    fn test_match_recipe_contains() {
        let recipe = WorkflowRecipe {
            id: "deploy".to_owned(),
            name: "Deploy".to_owned(),
            trigger_pattern: "deploy to production".to_owned(),
            steps: vec![],
        };
        let engine = RecipeEngine::new();
        let recipes_clone = [recipe.clone()];
        let matched = engine.match_recipe("please deploy to production now", &recipes_clone);
        assert!(matched.is_some());
        let recipes_final = [recipe];
        let no_match = engine.match_recipe("deploy to staging", &recipes_final);
        assert!(no_match.is_none());
    }

    #[test]
    fn test_match_recipe_empty_pattern_never_matches() {
        let recipe = WorkflowRecipe {
            id: "manual".to_owned(),
            name: "Manual".to_owned(),
            trigger_pattern: String::new(),
            steps: vec![],
        };
        let engine = RecipeEngine::new();
        let recipes = [recipe];
        let matched = engine.match_recipe("anything", &recipes);
        assert!(matched.is_none());
    }

    #[test]
    fn test_slug_conversion() {
        assert_eq!(slug("Plan then Execute"), "plan-then-execute");
        assert_eq!(slug("  foo  bar  "), "foo-bar");
    }

    #[test]
    fn test_forge_then_crunch_has_two_steps() {
        let r = forge_crunch();
        assert_eq!(r.steps.len(), 2);
        assert_eq!(
            r.steps[0].params.get("mode").map(String::as_str),
            Some("FORGE")
        );
        assert_eq!(
            r.steps[1].params.get("mode").map(String::as_str),
            Some("CRUNCH")
        );
    }
}
