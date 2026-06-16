/**
 * Purpose: Zod schemas for all ClawDE desktop forms.
 * Inputs:  Form data from user inputs (new project, OAuth setup, server-mode config).
 * Outputs: Validated and typed form data; inline field errors on invalid input.
 * Constraints: All schemas show errors only on blur/submit, never on initial render.
 *              Uses Zod from root workspace (no separate install needed in desktop).
 * SPORT: T-P3-E5-W1-S2-T02
 */

import { z } from 'zod';
import type { OAuthProvider } from '@/types';

// ── New Project form ──────────────────────────────────────────────────────────

/**
 * newProjectSchema — validates project creation input.
 * - name: 1-50 chars, non-empty after trim
 * - path: non-empty, must look like an absolute directory path
 */
export const newProjectSchema = z.object({
  name: z
    .string()
    .min(1, 'Project name cannot be empty')
    .max(50, 'Project name cannot exceed 50 characters')
    .refine((v) => v.trim().length > 0, 'Project name cannot be only whitespace'),
  path: z
    .string()
    .min(1, 'Project path cannot be empty')
    .refine(
      (v) => v.startsWith('/') || /^[A-Za-z]:\\/.test(v),
      'Path must be an absolute directory path (e.g. /home/user/project)'
    ),
});

export type NewProjectFormData = z.infer<typeof newProjectSchema>;

// ── OAuth setup form ──────────────────────────────────────────────────────────

const OAUTH_PROVIDERS: [OAuthProvider, ...OAuthProvider[]] = ['google', 'github', 'anthropic'];

/**
 * oauthSetupSchema — validates adding a new OAuth account.
 * - provider: must be one of the supported providers
 * The calling component is responsible for checking that the provider isn't
 * already added at free-tier capacity (N=1); that gate is enforced in the UI
 * and the daemon, not the schema.
 */
export const oauthSetupSchema = z.object({
  provider: z.enum(OAUTH_PROVIDERS, {
    errorMap: () => ({ message: 'Please select a valid OAuth provider (google, github, or anthropic)' }),
  }),
});

export type OAuthSetupFormData = z.infer<typeof oauthSetupSchema>;

// ── Server-mode config form ────────────────────────────────────────────────────

/**
 * serverModeSchema — validates server-mode configuration.
 * - host: valid hostname or IP (no spaces, no protocol)
 * - port: integer 1024-65535
 */
export const serverModeSchema = z.object({
  host: z
    .string()
    .min(1, 'Host cannot be empty')
    .refine(
      (v) =>
        /^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$/.test(v) ||
        /^(\d{1,3}\.){3}\d{1,3}$/.test(v),
      'Host must be a valid hostname or IP address'
    ),
  port: z
    .number({ invalid_type_error: 'Port must be a number' })
    .int('Port must be a whole number')
    .min(1024, 'Port must be at least 1024')
    .max(65535, 'Port cannot exceed 65535'),
});

export type ServerModeFormData = z.infer<typeof serverModeSchema>;
