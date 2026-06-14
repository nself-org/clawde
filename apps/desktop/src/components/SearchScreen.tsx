/**
 * Purpose: Search screen — full-text search across session messages and memory.
 * Inputs:  Search query; sessions/messages from conversationStore
 * Outputs: Ranked search results linking back to sessions
 * Constraints: Client-side search over loaded messages in v1; Cmd+K and Cmd+P open this
 * SPORT: T-E1-07
 */

import { useState, useRef, useEffect, useMemo } from "react";
import { Search, MessageSquare } from "lucide-react";
import { useConversationStore } from "@/stores/conversationStore";
import { useAppStore } from "@/stores/appStore";

interface SearchResult {
  sessionId: string;
  sessionTitle: string;
  snippet: string;
}

export function SearchScreen() {
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const { sessions, messages, setActiveSession } = useConversationStore(
    (s) => ({
      sessions: s.sessions,
      messages: s.messages,
      setActiveSession: s.setActiveSession,
    })
  );
  const setRoute = useAppStore((s) => s.setRoute);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const results = useMemo<SearchResult[]>(() => {
    if (!query.trim()) return [];
    const q = query.toLowerCase();
    const hits: SearchResult[] = [];
    for (const session of sessions) {
      const msgs = messages[session.id] ?? [];
      for (const msg of msgs) {
        if (msg.content?.toLowerCase().includes(q)) {
          const idx = msg.content.toLowerCase().indexOf(q);
          const snippet = msg.content.slice(Math.max(0, idx - 40), idx + 80);
          hits.push({
            sessionId: session.id,
            sessionTitle: session.title ?? `Session ${session.id.slice(0, 8)}`,
            snippet,
          });
          break; // one result per session
        }
      }
    }
    return hits;
  }, [query, sessions, messages]);

  const handleOpen = (sessionId: string) => {
    const session = sessions.find((s) => s.id === sessionId);
    if (session) {
      setActiveSession(session);
      setRoute("chat");
    }
  };

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <h1 className="text-lg font-semibold text-gray-100 mb-4">Search</h1>

      <div
        className="flex items-center gap-2 rounded-xl border px-3 py-2 mb-4"
        style={{ borderColor: "#2d3748", background: "#111827" }}
      >
        <Search size={16} className="text-gray-500 flex-shrink-0" />
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search sessions and messages…"
          className="flex-1 bg-transparent text-sm text-gray-100 placeholder-gray-600 focus:outline-none"
        />
      </div>

      {query && results.length === 0 && (
        <div className="text-gray-600 text-sm text-center mt-16">
          No results for "{query}"
        </div>
      )}

      <div className="space-y-2 overflow-y-auto">
        {results.map((r, i) => (
          <button
            key={i}
            onClick={() => handleOpen(r.sessionId)}
            className="w-full text-left px-4 py-3 rounded-xl border hover:bg-gray-900 transition-colors"
            style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
          >
            <div className="flex items-center gap-2 mb-1">
              <MessageSquare size={12} className="text-blue-400 flex-shrink-0" />
              <span className="text-sm font-medium text-gray-200 truncate">
                {r.sessionTitle}
              </span>
            </div>
            <div className="text-xs text-gray-500 line-clamp-2">
              …{r.snippet}…
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
