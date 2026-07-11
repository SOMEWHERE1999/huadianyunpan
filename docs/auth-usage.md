# Auth Usage

## Commands

### hddctl login

Starts an interactive login flow. Currently prompts for a Bearer token
pasted from the browser. Future: opens a WebView2 embedded browser.

```powershell
.\bin\hddctl.exe login
```

Supported login modes:
- **Console** (default): Prompts for Bearer token copied from browser DevTools
- **WebView2** (future): Opens embedded browser window

### hddctl auth status

Displays authentication status without revealing cookie/token values.

```powershell
.\bin\hddctl.exe auth status
```

Output:
```
Server: pan.ncepu.edu.cn
Authenticated: true
User: (if available)
Expires: (if available)
```

### hddctl logout

Clears locally stored credentials.

```powershell
.\bin\hddctl.exe logout
```

## WinFsp Mount Auth Check

Before mounting with the Huadian provider, hddsyncd loads the stored
session. If no valid session exists, AnyShare operations return
`ErrInteractiveLoginRequired`. The mount still succeeds but cloud
reads will fail with a clear error.

To check auth before mounting:
```powershell
.\bin\hddctl.exe auth status
```

## Common Errors

| Error | Cause | Fix |
|---|---|---|
| `ErrInteractiveLoginRequired` | No saved credentials | `hddctl login` |
| `ErrSessionExpired` | Token expired | `hddctl login` |
| `ErrWebViewUnavailable` | WebView2 not installed | Use console login |
| `ErrCookieExtractFailed` | Could not extract cookies | Use console login |

## Security

- Credentials stored at `%LOCALAPPDATA%\HuadianDrive\auth.json`
- Tokens and cookies are redacted in all log output
- Passwords, SMS codes, and CAS tickets are never stored
- TLS certificate validation is always enforced
