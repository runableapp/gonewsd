@echo off
setlocal EnableExtensions

REM
REM Copyright (c) 2026 Runable.app. GPL-3.0.
REM Interactive admin CLI for gonewsd auth (Windows CMD).
REM

set "BINDIR=%~dp0"
set "GONEWSD=%BINDIR%gonewsd.exe"

if not exist "%GONEWSD%" (
  if exist "%ProgramFiles%\gonewsd\gonewsd.exe" (
    set "GONEWSD=%ProgramFiles%\gonewsd\gonewsd.exe"
  ) else (
    echo Build or install gonewsd first: go build -o bin\gonewsd.exe .\cmd\gonewsd
    exit /b 1
  )
)

:banner
echo.
echo   ==============================================
echo   gonewsd admin menu
echo   ==============================================
"%GONEWSD%" version 2>NUL
echo.

:menu
echo   1^) listuser     - list users and their groups
echo   2^) adduser      - add a user
echo   3^) deleteuser   - remove a user
echo   4^) updateuser   - update user fields
echo   5^) listgroup    - list groups and permissions
echo   6^) addgroup     - add a newsgroup
echo   7^) deletegroup  - delete or archive a group
echo   8^) updategroup  - update group settings
echo.
echo   m^) show this menu
echo   q^) quit
echo.

:loop
set "input="
set /p "input=gonewsd (1:list 2:add 3:del 4:upd user ^| 5:list 6:add 7:del 8:upd group ^| m q^)^> "

if /I "%input%"=="1" "%GONEWSD%" listuser -format pretty
if /I "%input%"=="2" "%GONEWSD%" adduser
if /I "%input%"=="3" "%GONEWSD%" deleteuser
if /I "%input%"=="4" "%GONEWSD%" updateuser
if /I "%input%"=="5" "%GONEWSD%" listgroup -format pretty
if /I "%input%"=="6" "%GONEWSD%" addgroup
if /I "%input%"=="7" "%GONEWSD%" deletegroup
if /I "%input%"=="8" "%GONEWSD%" updategroup
if /I "%input%"=="m" goto menu
if /I "%input%"=="q" goto bye
if not "%input%"=="" echo Unknown option: %input%

echo.
goto loop

:bye
echo Bye.
exit /b 0
