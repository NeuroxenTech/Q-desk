// Q-DESK native shell layer.
//
// This module is the Rust-only boundary between the webview and the OS:
//   - Windows screen-capture protection (WDA_EXCLUDEFROMCAPTURE), so evidence
//     content in the app window never appears in screenshots / recordings /
//     screen-share tools while remaining visible on the physical display.
//   - Platform detection so the frontend can show accurate messaging.
//   - Secure in-memory session state (ticket id + session key). It is never
//     written to a plaintext file and never persisted via webview storage.
//     The active session is wiped on breach, on ticket expiry (frontend calls
//     clear_session) and automatically when the window closes.
//
// No evidence-fetching or UI logic lives here yet — commands only, plus the
// setup() hook that enables screen protection before any evidence content can
// be requested from the backend.

use std::io::Write;
use std::sync::Mutex;

use tauri::Manager;

#[cfg(target_os = "windows")]
use windows::Win32::Foundation::HWND;
#[cfg(target_os = "windows")]
use windows::Win32::UI::WindowsAndMessaging::{
    GetWindowDisplayAffinity, SetWindowDisplayAffinity, WDA_EXCLUDEFROMCAPTURE,
};

/// Machine-readable value of WDA_EXCLUDEFROMCAPTURE (0x00000011). Used to
/// interpret the DWORD reported by GetWindowDisplayAffinity.
#[cfg(target_os = "windows")]
const WDA_EXCLUDEFROMCAPTURE_BITS: u32 = 0x0000_0011;

/// Application state held only in memory (never persisted).
struct AppState {
    /// Active evidence session. `None` when no evidence is open.
    session: Mutex<Option<SessionInfo>>,
    /// Snapshot taken at startup: whether screen protection was enabled on the
    /// main window. The authoritative value is always queried live via
    /// `get_screen_protection_status`.
    screen_protection_active: Mutex<bool>,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            session: Mutex::new(None),
            screen_protection_active: Mutex::new(false),
        }
    }
}

/// In-memory evidence session. Serialized over the command bridge only; never
/// written to disk.
#[derive(Clone, serde::Serialize, serde::Deserialize)]
struct SessionInfo {
    ticket_id: String,
    /// Raw session key material agreed with the backend during the
    /// ML-KEM-768 handshake (encoded as base64).
    session_key_b64: String,
}

/// Appends a timestamped line to the platform log directory
/// (`(app_log_dir)/debug.log`). Never logs the window handle.
fn debug_log(app: &tauri::AppHandle, msg: &str) {
    let base_dir = match app.path().app_log_dir() {
        Ok(dir) => dir,
        Err(e) => {
            eprintln!("[q-desk] debug log unavailable: {e}");
            return;
        }
    };
    if let Err(e) = std::fs::create_dir_all(&base_dir) {
        eprintln!("[q-desk] cannot create log dir: {e}");
        return;
    }
    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    if let Ok(mut file) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(base_dir.join("debug.log"))
    {
        let _ = writeln!(file, "[{ts}] {msg}");
    }
}

/// Resolves the native window handle, converted to the `windows` crate's
/// HWND (0.58) used by the Win32 display-affinity FFI. tauri's `hwnd()`
/// returns its own HWND type (a `pub struct HWND(pub *mut c_void)`), so we
/// re-wrap the raw pointer directly.
#[cfg(target_os = "windows")]
fn window_hwnd(window: &tauri::Window) -> Result<HWND, String> {
    let hwnd = window
        .hwnd()
        .map_err(|e| format!("failed to get native window handle: {e}"))?;
    Ok(HWND(hwnd.0))
}

/// Enables Windows screen-capture protection on the given window. On Windows
/// this calls SetWindowDisplayAffinity(hwnd, WDA_EXCLUDEFROMCAPTURE) so the
/// window content is excluded from screenshots, recordings and screen-share
/// tools while remaining visible on the physical display.
#[tauri::command]
fn enable_screen_protection(window: tauri::Window) -> Result<(), String> {
    match enable_screen_protection_impl(&window) {
        Ok(()) => {
            debug_log(
                window.app_handle(),
                "screen protection enabled (WDA_EXCLUDEFROMCAPTURE)",
            );
            Ok(())
        }
        Err(e) => {
            debug_log(
                window.app_handle(),
                &format!("screen protection unavailable: {e}"),
            );
            Err(e)
        }
    }
}

#[cfg(target_os = "windows")]
fn enable_screen_protection_impl(window: &tauri::Window) -> Result<(), String> {
    let hwnd = window_hwnd(window)?;
    unsafe {
        SetWindowDisplayAffinity(hwnd, WDA_EXCLUDEFROMCAPTURE)
            .map_err(|e| format!("SetWindowDisplayAffinity failed: {e}"))?;
    }
    Ok(())
}

#[cfg(not(target_os = "windows"))]
fn enable_screen_protection_impl(_window: &tauri::Window) -> Result<(), String> {
    Err("Screen protection is not supported on this platform: WDA_EXCLUDEFROMCAPTURE is Windows-only. Evidence viewing is disabled on this device.".to_string())
}

/// Reports whether screen-capture protection is currently active on the given
/// window, queried live from the OS rather than assumed.
#[tauri::command]
fn get_screen_protection_status(window: tauri::Window) -> Result<bool, String> {
    get_screen_protection_status_impl(&window)
}

#[cfg(target_os = "windows")]
fn get_screen_protection_status_impl(window: &tauri::Window) -> Result<bool, String> {
    let hwnd = window_hwnd(window)?;
    let mut affinity: u32 = 0;
    unsafe {
        GetWindowDisplayAffinity(hwnd, &mut affinity)
            .map_err(|e| format!("GetWindowDisplayAffinity failed: {e}"))?;
    }
    Ok(affinity == WDA_EXCLUDEFROMCAPTURE_BITS)
}

#[cfg(not(target_os = "windows"))]
fn get_screen_protection_status_impl(_window: &tauri::Window) -> Result<bool, String> {
    Ok(false)
}

/// Returns the target platform as 'windows', 'macos' or 'linux' so the
/// frontend can show platform-appropriate messaging (WDA_EXCLUDEFROMCAPTURE
/// is Windows-only).
#[tauri::command]
fn get_platform() -> String {
    #[cfg(target_os = "windows")]
    {
        return "windows".to_string();
    }
    #[cfg(target_os = "macos")]
    {
        return "macos".to_string();
    }
    #[cfg(target_os = "linux")]
    {
        return "linux".to_string();
    }
    #[allow(unreachable_code)]
    "unsupported".to_string()
}

/// Stores the evidence session (ticket id + session key) in memory only.
/// The frontend must call this right after the ML-KEM handshake, and must
/// never persist either value in localStorage/sessionStorage.
#[tauri::command]
fn set_session(
    state: tauri::State<'_, AppState>,
    ticket_id: String,
    session_key_b64: String,
) -> Result<(), String> {
    let mut guard = state
        .session
        .lock()
        .map_err(|_| "session state poisoned".to_string())?;
    *guard = Some(SessionInfo {
        ticket_id,
        session_key_b64,
    });
    Ok(())
}

/// Returns the current in-memory session, if any. Only transported over the
/// command bridge; never persisted.
#[tauri::command]
fn get_session(state: tauri::State<'_, AppState>) -> Result<Option<SessionInfo>, String> {
    let guard = state
        .session
        .lock()
        .map_err(|_| "session state poisoned".to_string())?;
    Ok(guard.clone())
}

/// Wipes the in-memory session. The frontend calls this on breach, on ticket
/// expiry, and on app close (it is also wiped automatically on window close).
#[tauri::command]
fn clear_session(state: tauri::State<'_, AppState>) -> Result<(), String> {
    let mut guard = state
        .session
        .lock()
        .map_err(|_| "session state poisoned".to_string())?;
    *guard = None;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(AppState::default())
        .invoke_handler(tauri::generate_handler![
            enable_screen_protection,
            get_screen_protection_status,
            get_platform,
            set_session,
            get_session,
            clear_session,
        ])
        .setup(|app| {
            if let Some(webview_window) = app.get_webview_window("main") {
                let window = webview_window.as_ref().window();
                match enable_screen_protection(window) {
                    Ok(()) => {
                        debug_log(
                            app.handle(),
                            "startup: screen protection enabled on main window",
                        );
                        if let Some(state) = app.try_state::<AppState>() {
                            if let Ok(mut active) = state.screen_protection_active.lock() {
                                *active = true;
                            }
                        }
                    }
                    Err(e) => {
                        // Do not proceed to the evidence view: the frontend must
                        // query get_screen_protection_status() and show a blocking
                        // error screen when protection is unavailable.
                        debug_log(
                            app.handle(),
                            &format!("startup: screen protection unavailable: {e}"),
                        );
                        eprintln!("[q-desk] screen protection unavailable: {e}");
                        if let Some(state) = app.try_state::<AppState>() {
                            if let Ok(mut active) = state.screen_protection_active.lock() {
                                *active = false;
                            }
                        }
                    }
                }
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            use tauri::WindowEvent;
            // Never leave the ticket/session key resident after the window is gone.
            if matches!(
                event,
                WindowEvent::CloseRequested { .. } | WindowEvent::Destroyed
            ) {
                if let Some(state) = window.app_handle().try_state::<AppState>() {
                    if let Ok(mut session) = state.session.lock() {
                        *session = None;
                    }
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
