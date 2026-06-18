/**
 * Purpose: OAuth account pool management — add/remove N OAuth accounts per provider.
 * Inputs:  Daemon OAuth API via Tauri invoke; bundle license check (N>1 gated).
 * Outputs: Grid of current accounts with provider badge + Add/Remove actions.
 * Constraints: Free tier limited to 1 account per provider (license_required error
 *              surfaced inline). Bundle members (ClawDE $0.99/mo) get unlimited N.
 * SPORT: T-P3-E5-W1-S2-T02
 */

import React, { useEffect, useState, useCallback } from "react";
import {
  KeyRound, Plus, Trash2, RefreshCw, ShieldAlert,
  Github, Bot, Chrome, Loader2, AlertCircle, CheckCircle2,
} from "lucide-react";
import { listOAuthAccounts, addOAuthAccount, removeOAuthAccount } from "@/lib/tauriApi";
import type { OAuthAccount, OAuthProvider } from "@/types";
import { oauthSetupSchema } from "@/lib/validation/schemas";
import type { OAuthSetupFormData } from "@/lib/validation/schemas";

// ── Provider metadata ──────────────────────────────────────────────────────────

const PROVIDER_META: Record<OAuthProvider, { label: string; icon: React.ReactNode; color: string }> = {
  google: {
    label: "Google",
    icon: <Chrome size={16} />,
    color: "text-blue-400 bg-blue-900/30 border-blue-800",
  },
  github: {
    label: "GitHub",
    icon: <Github size={16} />,
    color: "text-gray-300 bg-gray-800/50 border-gray-700",
  },
  anthropic: {
    label: "Anthropic",
    icon: <Bot size={16} />,
    color: "text-orange-400 bg-orange-900/30 border-orange-800",
  },
};

const PROVIDERS: OAuthProvider[] = ["google", "github", "anthropic"];

// ── Sub-components ─────────────────────────────────────────────────────────────

function ProviderBadge({ provider }: { provider: OAuthProvider }) {
  const meta = PROVIDER_META[provider];
  return (
    <span
      className={[
        "inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs border",
        meta.color,
      ].join(" ")}
    >
      {meta.icon}
      {meta.label}
    </span>
  );
}

function AccountCard({
  account,
  onRemove,
  removing,
}: {
  account: OAuthAccount;
  onRemove: (id: string) => void;
  removing: boolean;
}) {
  return (
    <div
      className="flex items-center justify-between gap-3 px-4 py-3 rounded-lg border"
      style={{ background: "#0f1724", borderColor: "#1e2638" }}
    >
      <div className="flex items-center gap-3 min-w-0">
        <ProviderBadge provider={account.provider} />
        <div className="min-w-0">
          <div className="text-sm text-gray-200 truncate">{account.email}</div>
          {account.displayName && (
            <div className="text-xs text-gray-500 truncate">{account.displayName}</div>
          )}
        </div>
      </div>
      <div className="flex items-center gap-2 flex-shrink-0">
        <CheckCircle2 size={14} className="text-green-500" />
        <span className="text-xs text-gray-500">
          {new Date(account.addedAt).toLocaleDateString()}
        </span>
        <button
          onClick={() => onRemove(account.id)}
          disabled={removing}
          className="p-1.5 rounded text-gray-500 hover:text-red-400 hover:bg-red-900/20 transition-colors disabled:opacity-50"
          aria-label="Remove account"
          title="Remove account"
        >
          {removing ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
        </button>
      </div>
    </div>
  );
}

// ── AddAccountForm ─────────────────────────────────────────────────────────────

function AddAccountForm({
  onAdded,
  existingProviders,
}: {
  onAdded: (account: OAuthAccount) => void;
  existingProviders: Set<OAuthProvider>;
}) {
  const [provider, setProvider] = useState<OAuthProvider | "">("");
  const [error, setError] = useState<string | null>(null);
  const [licenseError, setLicenseError] = useState(false);
  const [adding, setAdding] = useState(false);

  const handleAdd = async () => {
    setError(null);
    setLicenseError(false);

    // Zod validation
    const result = oauthSetupSchema.safeParse({ provider } as OAuthSetupFormData);
    if (!result.success) {
      setError(result.error.errors[0]?.message ?? "Invalid provider");
      return;
    }

    setAdding(true);
    try {
      const account = await addOAuthAccount(provider);
      onAdded(account);
      setProvider("");
    } catch (e) {
      const msg = String(e);
      if (msg.includes("license_required") || msg.includes("bundle required")) {
        setLicenseError(true);
      } else {
        setError(msg);
      }
    } finally {
      setAdding(false);
    }
  };

  return (
    <div
      className="rounded-lg border p-4"
      style={{ background: "#0f1724", borderColor: "#1e2638" }}
    >
      <div className="text-sm font-medium text-gray-300 mb-3">Add OAuth Account</div>

      {licenseError && (
        <div className="flex items-start gap-2 p-3 rounded-lg bg-orange-900/20 border border-orange-800 mb-3">
          <ShieldAlert size={15} className="text-orange-400 flex-shrink-0 mt-0.5" />
          <div className="text-xs text-orange-300">
            Multiple accounts per provider require the{" "}
            <span className="font-semibold">ClawDE bundle</span> ($0.99/mo).
            Run <code className="font-mono bg-orange-900/30 px-1 rounded">nself license set &lt;key&gt;</code> to upgrade.
          </div>
        </div>
      )}

      <div className="flex items-end gap-2">
        <div className="flex-1">
          <label className="block text-xs text-gray-500 mb-1">Provider</label>
          <select
            value={provider}
            onChange={(e) => {
              setProvider(e.target.value as OAuthProvider | "");
              setError(null);
              setLicenseError(false);
            }}
            className={[
              "w-full px-3 py-2 rounded-lg text-sm text-gray-200 border outline-none",
              "focus:ring-2 focus:ring-blue-500",
              error ? "border-red-500 bg-red-900/10" : "border-gray-700 bg-gray-900",
            ].join(" ")}
          >
            <option value="">Select provider…</option>
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {PROVIDER_META[p].label}
                {existingProviders.has(p) ? " (already added)" : ""}
              </option>
            ))}
          </select>
          {error && <div className="text-xs text-red-400 mt-1">{error}</div>}
        </div>

        <button
          onClick={handleAdd}
          disabled={adding || !provider}
          className={[
            "flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors",
            "focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500",
            adding || !provider
              ? "bg-gray-700 text-gray-500 cursor-not-allowed"
              : "bg-blue-600 text-white hover:bg-blue-500",
          ].join(" ")}
        >
          {adding ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
          Connect
        </button>
      </div>
      <p className="text-xs text-gray-600 mt-2">
        Connecting opens a browser window to complete the OAuth flow.
      </p>
    </div>
  );
}

// ── Main screen ────────────────────────────────────────────────────────────────

export function OAuthPoolScreen() {
  const [accounts, setAccounts] = useState<OAuthAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const list = await listOAuthAccounts();
      setAccounts(list);
    } catch (e) {
      setLoadError(String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const handleRemove = async (id: string) => {
    setRemovingId(id);
    try {
      await removeOAuthAccount(id);
      setAccounts((prev) => prev.filter((a) => a.id !== id));
    } catch (e) {
      setLoadError(String(e));
    } finally {
      setRemovingId(null);
    }
  };

  const handleAdded = (account: OAuthAccount) => {
    setAccounts((prev) => [...prev, account]);
  };

  // Group accounts by provider
  const byProvider = new Map<OAuthProvider, OAuthAccount[]>();
  for (const account of accounts) {
    const list = byProvider.get(account.provider) ?? [];
    list.push(account);
    byProvider.set(account.provider, list);
  }
  const existingProviders = new Set<OAuthProvider>(byProvider.keys());

  return (
    <div
      className="flex flex-col h-full overflow-y-auto"
      style={{ background: "#030712" }}
    >
      {/* Header */}
      <div
        className="flex items-center justify-between px-5 py-4 border-b flex-shrink-0"
        style={{ borderColor: "#1e2638" }}
      >
        <div className="flex items-center gap-2">
          <KeyRound size={18} className="text-blue-400" />
          <span className="text-base font-semibold text-gray-100">OAuth Accounts</span>
          {accounts.length > 0 && (
            <span className="ml-1 text-xs bg-gray-800 text-gray-400 px-1.5 py-0.5 rounded-full">
              {accounts.length}
            </span>
          )}
        </div>
        <button
          onClick={refresh}
          disabled={loading}
          className="p-1.5 rounded text-gray-500 hover:text-gray-300 hover:bg-gray-800 transition-colors disabled:opacity-50"
          title="Refresh"
        >
          <RefreshCw size={15} className={loading ? "animate-spin" : ""} />
        </button>
      </div>

      <div className="flex-1 p-5 space-y-4">
        {loadError && (
          <div className="flex items-start gap-2 p-3 rounded-lg bg-red-900/20 border border-red-800">
            <AlertCircle size={15} className="text-red-400 flex-shrink-0 mt-0.5" />
            <span className="text-xs text-red-300">{loadError}</span>
          </div>
        )}

        {/* Add form */}
        <AddAccountForm onAdded={handleAdded} existingProviders={existingProviders} />

        {/* Account list */}
        {loading ? (
          <div className="flex items-center justify-center py-10 text-gray-600">
            <Loader2 size={20} className="animate-spin mr-2" />
            <span className="text-sm">Loading accounts…</span>
          </div>
        ) : accounts.length === 0 ? (
          <div className="text-center py-10 text-gray-600">
            <KeyRound size={32} className="mx-auto mb-3 opacity-30" />
            <p className="text-sm">No OAuth accounts connected.</p>
            <p className="text-xs mt-1">Add a provider above to get started.</p>
          </div>
        ) : (
          <div className="space-y-2">
            <div className="text-xs text-gray-500 mb-1">Connected accounts</div>
            {accounts.map((account) => (
              <AccountCard
                key={account.id}
                account={account}
                onRemove={handleRemove}
                removing={removingId === account.id}
              />
            ))}
          </div>
        )}

        {/* ClawDE bundle info */}
        <div
          className="flex items-start gap-3 p-3 rounded-lg border"
          style={{ background: "#0a0e1a", borderColor: "#1e2638" }}
        >
          <ShieldAlert size={15} className="text-gray-600 flex-shrink-0 mt-0.5" />
          <div className="text-xs text-gray-600">
            <span className="text-gray-500">Free tier:</span> 1 account per provider.{" "}
            <span className="text-gray-500">ClawDE bundle ($0.99/mo):</span> unlimited accounts per provider.
          </div>
        </div>
      </div>
    </div>
  );
}
