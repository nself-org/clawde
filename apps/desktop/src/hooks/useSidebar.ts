/**
 * Purpose: Sidebar open/close state for ChatScreen session list panel.
 * Inputs:  None (self-contained UI state)
 * Outputs: isOpen boolean + toggle/open/close actions
 * Constraints: Persisted to localStorage so state survives app restarts
 * SPORT: T-E1-07
 */

import { useState, useCallback, useEffect } from "react";

const STORAGE_KEY = "clawde-sidebar-open";

export function useSidebar(defaultOpen = true) {
  const [isOpen, setIsOpen] = useState<boolean>(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      return stored !== null ? JSON.parse(stored) as boolean : defaultOpen;
    } catch {
      return defaultOpen;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(isOpen));
    } catch {
      // localStorage may be unavailable in some environments
    }
  }, [isOpen]);

  const toggle = useCallback(() => setIsOpen((v) => !v), []);
  const open = useCallback(() => setIsOpen(true), []);
  const close = useCallback(() => setIsOpen(false), []);

  return { isOpen, toggle, open, close };
}
