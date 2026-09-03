import { invoke } from "@tauri-apps/api/core";

export async function getPlatform(): Promise<string> {
  return invoke<string>("get_platform");
}

export async function enableScreenProtection(): Promise<void> {
  return invoke<void>("enable_screen_protection");
}

export async function getScreenProtectionStatus(): Promise<boolean> {
  return invoke<boolean>("get_screen_protection_status");
}

export async function setSession(
  ticketId: string,
  sessionKeyB64: string
): Promise<void> {
  return invoke<void>("set_session", {
    ticketId: ticketId,
    sessionKeyB64: sessionKeyB64,
  });
}

export async function getSession(): Promise<{
  ticket_id: string;
  session_key_b64: string;
} | null> {
  return invoke<{ ticket_id: string; session_key_b64: string } | null>(
    "get_session"
  );
}

export async function clearSession(): Promise<void> {
  return invoke<void>("clear_session");
}
