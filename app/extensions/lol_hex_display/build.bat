@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

echo Checking LOL Hex extension sources...
if not exist "launcher.py" exit /b 1
if not exist "main.py" exit /b 1
if not exist "bar_integration.py" exit /b 1
if not exist "templates\manager.html" exit /b 1
if not exist "templates\display.html" exit /b 1
if not exist "templates\bar.html" exit /b 1
if not exist "static\style.css" exit /b 1

where python >nul 2>nul
if errorlevel 1 (
  echo Python 3.10+ is required.
  exit /b 1
)
python -c "import sys; raise SystemExit(0 if sys.version_info >= (3,10) else 1)"
if errorlevel 1 exit /b 1

echo Checking build dependencies...
python -c "import PyInstaller, flask, requests, PIL, rapidocr_onnxruntime, onnxruntime, mss, keyboard"
if errorlevel 1 (
  echo Install dependencies with: python -m pip install -r requirements.txt
  exit /b 1
)

echo Building LOL Hex standalone directory...
python -m PyInstaller --noconfirm --clean --onedir --noconsole --manifest "lol_hex_display.manifest" --name "lol_hex_display" --collect-all rapidocr_onnxruntime --collect-all onnxruntime --hidden-import keyboard --hidden-import mss --hidden-import tkinter "launcher.py"
if errorlevel 1 exit /b 1

xcopy /E /I /Y "templates" "dist\lol_hex_display\templates" >nul
if errorlevel 1 exit /b 1
xcopy /E /I /Y "static" "dist\lol_hex_display\static" >nul
if errorlevel 1 exit /b 1

if not exist "dist\lol_hex_display\lol_hex_display.exe" exit /b 1
echo Build complete: dist\lol_hex_display\lol_hex_display.exe
