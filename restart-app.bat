@echo off
setlocal enabledelayedexpansion
setlocal

set "ROOT=C:\code base\Q-DESK"

echo ============================================================
echo  Q-DESK restart (dev): backend + Next dev server + app
echo ============================================================

echo.
echo [1/5] Stopping running Q-DESK...
taskkill /IM q-desk.exe /F >nul 2>&1

echo [2/5] Restarting backend...
taskkill /IM qdesk_backend.exe /F >nul 2>&1
ping -n 2 127.0.0.1 >nul
rem Load .env into this process so the backend inherits it.
if exist "%ROOT%\.env" (
  for /f "usebackq tokens=1,* delims==" %%a in ("%ROOT%\.env") do (
    if not "%%a"=="" (
      set "line=%%a"
      if not "!line:~0,1!"=="#" set "%%a=%%b"
    )
  )
)
start "qdesk-backend" /min cmd /c "cd /d "%ROOT%\backend" && qdesk_backend.exe > "%TEMP%\qdesk_backend.log" 2>&1"

echo [3/5] Stopping old Next dev server on port 3000...
for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":3000 " ^| findstr LISTENING') do (
  taskkill /PID %%a /F >nul 2>&1
)
ping -n 3 127.0.0.1 >nul

echo [4/5] Starting Next dev server...
start "next-dev" /min cmd /c "cd /d "%ROOT%\src" && npm run dev > "%TEMP%\qdesk_frontend.log" 2>&1"

echo      waiting for dev server on :3000...
set "tries=0"
:wait
set /a tries+=1
if %tries% gtr 45 (
  echo      WARNING: dev server not ready after ~90s, launching anyway.
  goto launch
)
ping -n 3 127.0.0.1 >nul
netstat -ano | findstr ":3000 " | findstr LISTENING >nul
if errorlevel 1 goto wait

:launch
echo [5/5] Launching Q-DESK...
start "" "%ROOT%\src-tauri\target\debug\q-desk.exe"

echo.
echo Done. Q-DESK is starting.
endlocal