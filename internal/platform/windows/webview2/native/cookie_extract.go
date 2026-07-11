//go:build windows && webview2

// Package native provides the CGo WebView2 cookie extraction adapter.
// It requires the WebView2 SDK (NuGet: Microsoft.Web.WebView2) to be installed.
//
// Build with: go build -tags webview2
package native

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lWebView2Loader

#include <stdlib.h>
#include <windows.h>
#include <wrl/client.h>
#include <WebView2.h>
#include <string>
#include <mutex>
#include <condition_variable>
#include <cstdio>
#include <cstring>

using namespace Microsoft::WRL;

// Minimal JSON builder
struct JSONBuf {
    std::string s;
    void openObj() { s += '{'; }
    void closeObj() {
        if (!s.empty() && s.back() == ',') s.pop_back();
        s += '}';
    }
    void openArr(const char* k) { key(k); s += '['; }
    void closeArr() {
        if (!s.empty() && s.back() == ',') s.pop_back();
        s += ']';
    }
    void key(const char* k) {
        s += '"'; s += k; s += "\":";
    }
    void strVal(const char* v) {
        s += '"';
        for (const char* p = v; *p; p++) {
            switch (*p) {
            case '"':  s += "\\\""; break;
            case '\\': s += "\\\\"; break;
            case '\n': s += "\\n";  break;
            case '\r': s += "\\r";  break;
            case '\t': s += "\\t";  break;
            default:   s += *p;
            }
        }
        s += '"'; s += ',';
    }
    void str(const char* k, const char* v) { key(k); strVal(v); }
    void str(const char* k, const std::wstring& w) {
        int len = WideCharToMultiByte(CP_UTF8, 0, w.c_str(), -1, NULL, 0, NULL, NULL);
        std::string u8(len - 1, '\0');
        WideCharToMultiByte(CP_UTF8, 0, w.c_str(), -1, &u8[0], len, NULL, NULL);
        str(k, u8.c_str());
    }
    void boolean(const char* k, bool v) { key(k); s += v ? "true," : "false,"; }
    void number(const char* k, double v) {
        key(k);
        char buf[64];
        snprintf(buf, sizeof(buf), "%.0f", v);
        s += buf; s += ',';
    }
    void integer(const char* k, int v) {
        key(k);
        char buf[32];
        snprintf(buf, sizeof(buf), "%d", v);
        s += buf; s += ',';
    }
    void setError(const char* msg) {
        clear();
        openObj();
        boolean("success", false);
        str("error", msg);
        s += "\"cookies\":[]";
        closeObj();
    }
    void clear() { s.clear(); }
};

// State shared between async callbacks and main thread
struct LoginState {
    std::mutex mtx;
    std::condition_variable cv;
    bool envReady = false;
    bool loginDetected = false;
    bool cancelled = false;
    HRESULT envHResult = S_OK;
    std::string resultJSON;

    ComPtr<ICoreWebView2Environment> env;
    ComPtr<ICoreWebView2Controller> controller;
    ComPtr<ICoreWebView2> webView;
    ComPtr<ICoreWebView2CookieManager> cookieManager;
    HWND hwnd = NULL;
    JSONBuf json;
};

// Forward declarations
static bool isOnTargetDomain(LoginState* state);
static void buildCookieJSON(LoginState* state, ICoreWebView2CookieList* cookieList);

// Navigation completed handler
struct NavCompletedHandler : public ICoreWebView2NavigationCompletedEventHandler {
    LoginState* state;
    NavCompletedHandler(LoginState* s) : state(s) { s->env->AddRef(); }
    ~NavCompletedHandler() { if (state) state->env->Release(); }
    volatile ULONG m_refCount = 1;

    ULONG STDMETHODCALLTYPE AddRef() override {
        return InterlockedIncrement(&m_refCount);
    }
    ULONG STDMETHODCALLTYPE Release() override {
        ULONG c = InterlockedDecrement(&m_refCount);
        if (c == 0) delete this;
        return c;
    }
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == __uuidof(IUnknown) || riid == __uuidof(ICoreWebView2NavigationCompletedEventHandler)) {
            *ppv = static_cast<ICoreWebView2NavigationCompletedEventHandler*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = NULL;
        return E_NOINTERFACE;
    }
    HRESULT STDMETHODCALLTYPE Invoke(ICoreWebView2* sender, ICoreWebView2NavigationCompletedEventArgs* args) override {
        if (!args) return S_OK;
        BOOL success = FALSE;
        args->get_IsSuccess(&success);
        if (success && isOnTargetDomain(state)) {
            state->loginDetected = true;
            if (state->cookieManager) {
                auto* handler = new (std::nothrow) GetCookiesHandler(state);
                if (handler) {
                    state->cookieManager->GetCookies(L"https://pan.ncepu.edu.cn/",
                        static_cast<ICoreWebView2GetCookiesCompletedHandler*>(handler));
                    handler->Release();
                }
            }
        }
        return S_OK;
    }
};

// Environment created callback
struct EnvReadyHandler : public ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler {
    LoginState* state;
    EnvReadyHandler(LoginState* s) : state(s) {}
    ULONG STDMETHODCALLTYPE AddRef() override { return 1; }
    ULONG STDMETHODCALLTYPE Release() override { return 1; }
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == __uuidof(IUnknown) || riid == __uuidof(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler)) {
            *ppv = static_cast<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler*>(this);
            return S_OK;
        }
        *ppv = NULL;
        return E_NOINTERFACE;
    }
    HRESULT STDMETHODCALLTYPE Invoke(HRESULT result, ICoreWebView2Environment* createdEnv) override {
        std::lock_guard<std::mutex> lock(state->mtx);
        state->envHResult = result;
        if (SUCCEEDED(result) && createdEnv) {
            state->env = createdEnv;
            state->env->AddRef();
        }
        state->envReady = true;
        state->cv.notify_one();
        return S_OK;
    }
};

// Cookie retrieval callback
struct GetCookiesHandler : public ICoreWebView2GetCookiesCompletedHandler {
    LoginState* state;
    GetCookiesHandler(LoginState* s) : state(s) {}
    ULONG STDMETHODCALLTYPE AddRef() override { return 1; }
    ULONG STDMETHODCALLTYPE Release() override { return 1; }
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == __uuidof(IUnknown) || riid == __uuidof(ICoreWebView2GetCookiesCompletedHandler)) {
            *ppv = static_cast<ICoreWebView2GetCookiesCompletedHandler*>(this);
            return S_OK;
        }
        *ppv = NULL;
        return E_NOINTERFACE;
    }
    HRESULT STDMETHODCALLTYPE Invoke(HRESULT result, ICoreWebView2CookieList* cookieList) override {
        std::lock_guard<std::mutex> lock(state->mtx);
        if (SUCCEEDED(result)) {
            buildCookieJSON(state, cookieList);
        } else {
            state->json.setError("failed to retrieve cookies from CookieManager");
            state->resultJSON = state->json.s;
        }
        if (state->hwnd) {
            PostMessageW(state->hwnd, WM_CLOSE, 0, 0);
        }
        state->cv.notify_one();
        return S_OK;
    }
};

// Window procedure
LRESULT CALLBACK WndProc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    LoginState* state = (LoginState*)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
    switch (msg) {
    case WM_CREATE:
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, (LONG_PTR)((CREATESTRUCT*)lp)->lpCreateParams);
        return 0;
    case WM_CLOSE:
        if (state) {
            std::lock_guard<std::mutex> lock(state->mtx);
            state->cancelled = true;
            state->cv.notify_one();
        }
        DestroyWindow(hwnd);
        return 0;
    case WM_DESTROY:
        PostQuitMessage(0);
        return 0;
    case WM_SIZE:
        if (state && state->controller) {
            RECT rc; GetClientRect(hwnd, &rc);
            state->controller->put_Bounds(rc);
        }
        return 0;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

static bool isOnTargetDomain(LoginState* state) {
    if (!state->webView) return false;
    wil::unique_cotaskmem_string uri;
    if (FAILED(state->webView->get_Source(&uri)) || !uri) return false;
    std::wstring url(uri.get());
    if (url.find(L"pan.ncepu.edu.cn") == std::wstring::npos) return false;
    if (url.find(L"ids.ncepu.edu.cn/authserver/login") != std::wstring::npos) return false;
    return true;
}

static void buildCookieJSON(LoginState* state, ICoreWebView2CookieList* cookieList) {
    state->json.clear();
    state->json.openObj();
    state->json.boolean("success", true);
    state->json.openArr("cookies");

    UINT count = 0;
    if (cookieList) cookieList->get_Count(&count);

    for (UINT i = 0; i < count; i++) {
        ComPtr<ICoreWebView2Cookie> cookie;
        if (FAILED(cookieList->GetValueAtIndex(i, &cookie)) || !cookie) continue;

        wil::unique_cotaskmem_string name, value, domain, path;
        double expires = 0;
        BOOL secure = FALSE, httpOnly = FALSE;
        COREWEBVIEW2_COOKIE_SAME_SITE_KIND sameSite = COREWEBVIEW2_COOKIE_SAME_SITE_KIND_NONE;

        cookie->get_Name(&name);
        cookie->get_Value(&value);
        cookie->get_Domain(&domain);
        cookie->get_Path(&path);
        cookie->get_Expires(&expires);
        cookie->get_IsSecure(&secure);
        cookie->get_IsHttpOnly(&httpOnly);
        cookie->get_SameSite(&sameSite);

        if (!name || !domain) continue;

        state->json.s += '{';
        state->json.str("name",     name.get());
        state->json.str("value",    value.get() ? value.get() : L"");
        state->json.str("domain",   domain.get());
        state->json.str("path",     path.get() ? path.get() : L"/");
        state->json.number("expires",    expires);
        state->json.boolean("secure",    secure);
        state->json.boolean("http_only", httpOnly);
        state->json.integer("same_site", (int)sameSite);
        if (!state->json.s.empty() && state->json.s.back() == ',')
            state->json.s.pop_back();
        state->json.s += "},";
    }

    state->json.closeArr();
    state->json.str("error", "");
    state->json.closeObj();
    state->resultJSON = state->json.s;
}

// Public C API
extern "C" {

char* extractCookiesC(const char* loginURL, int timeoutSeconds) {
    if (!loginURL || timeoutSeconds <= 0) {
        return _strdup("{\"success\":false,\"error\":\"invalid arguments\",\"cookies\":[]}");
    }

    LoginState state;

    int urlLen = MultiByteToWideChar(CP_UTF8, 0, loginURL, -1, NULL, 0);
    std::wstring wideURL(urlLen - 1, L'\0');
    MultiByteToWideChar(CP_UTF8, 0, loginURL, -1, &wideURL[0], urlLen);

    HINSTANCE hInst = GetModuleHandleW(NULL);
    const wchar_t* CLASS_NAME = L"HDDWebView2LoginWnd";

    WNDCLASSEXW wc = {};
    wc.cbSize = sizeof(WNDCLASSEXW);
    wc.lpfnWndProc = WndProc;
    wc.hInstance = hInst;
    wc.lpszClassName = CLASS_NAME;
    wc.style = CS_HREDRAW | CS_VREDRAW;
    RegisterClassExW(&wc);

    int screenW = GetSystemMetrics(SM_CXSCREEN);
    int screenH = GetSystemMetrics(SM_CYSCREEN);
    int wndW = (screenW * 3) / 4;
    int wndH = (screenH * 3) / 4;
    int wndX = (screenW - wndW) / 2;
    int wndY = (screenH - wndH) / 2;

    state.hwnd = CreateWindowExW(0, CLASS_NAME, L"Huadian Drive - Login",
        WS_OVERLAPPEDWINDOW, wndX, wndY, wndW, wndH,
        NULL, NULL, hInst, &state);

    if (!state.hwnd) {
        return _strdup("{\"success\":false,\"error\":\"failed to create window\",\"cookies\":[]}");
    }

    ShowWindow(state.hwnd, SW_SHOW);
    UpdateWindow(state.hwnd);

    // Create WebView2 environment
    ComPtr<EnvReadyHandler> envH(new EnvReadyHandler(&state));
    HRESULT hr = CreateCoreWebView2EnvironmentWithOptions(
        nullptr, nullptr, nullptr, envH.Get());
    if (FAILED(hr)) {
        DestroyWindow(state.hwnd);
        UnregisterClassW(CLASS_NAME, hInst);
        return _strdup("{\"success\":false,\"error\":\"CreateCoreWebView2EnvironmentWithOptions failed\",\"cookies\":[]}");
    }

    {
        std::unique_lock<std::mutex> lock(state.mtx);
        state.cv.wait(lock, [&state] { return state.envReady || state.cancelled; });
    }

    if (state.cancelled || FAILED(state.envHResult) || !state.env) {
        DestroyWindow(state.hwnd);
        UnregisterClassW(CLASS_NAME, hInst);
        if (state.cancelled)
            return _strdup("{\"success\":false,\"error\":\"login cancelled\",\"cookies\":[]}");
        return _strdup("{\"success\":false,\"error\":\"WebView2 environment creation failed\",\"cookies\":[]}");
    }

    // Create WebView2 controller
    state.env->CreateCoreWebView2Controller(state.hwnd,
        Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
            [&state](HRESULT result, ICoreWebView2Controller* ctrl) -> HRESULT {
                std::lock_guard<std::mutex> lock(state.mtx);
                if (SUCCEEDED(result) && ctrl) {
                    state.controller = ctrl;
                    state.controller->get_CoreWebView2(&state.webView);
                    if (state.webView) {
                        ComPtr<ICoreWebView2_2> wv2;
                        if (SUCCEEDED(state.webView.As(&wv2)) && wv2) {
                            wv2->get_CookieManager(&state.cookieManager);
                        }
                        ComPtr<NavCompletedHandler> navH(new NavCompletedHandler(&state));
                        EventRegistrationToken tok;
                        state.webView->add_NavigationCompleted(navH.Get(), &tok);
                    }
                }
                state.envReady = true;
                state.cv.notify_one();
                return S_OK;
            }).Get());

    {
        std::unique_lock<std::mutex> lock(state.mtx);
        state.envReady = false;
        state.cv.wait(lock, [&state] { return state.envReady || state.cancelled; });
    }

    if (state.cancelled || !state.webView) {
        DestroyWindow(state.hwnd);
        UnregisterClassW(CLASS_NAME, hInst);
        return _strdup("{\"success\":false,\"error\":\"failed to create WebView2 controller\",\"cookies\":[]}");
    }

    // Navigate and resize
    state.webView->Navigate(wideURL.c_str());
    {
        RECT rc; GetClientRect(state.hwnd, &rc);
        if (state.controller) state.controller->put_Bounds(rc);
    }

    // Message loop with timeout
    DWORD startTime = GetTickCount();
    DWORD timeoutMs = (DWORD)timeoutSeconds * 1000;

    while (!state.loginDetected && !state.cancelled) {
        DWORD elapsed = GetTickCount() - startTime;
        if (elapsed >= timeoutMs) break;

        DWORD remaining = timeoutMs - elapsed;
        DWORD waitResult = MsgWaitForMultipleObjects(0, NULL, FALSE,
            (remaining > 100) ? 100 : remaining, QS_ALLINPUT);

        if (waitResult == WAIT_OBJECT_0) {
            MSG msg;
            while (PeekMessageW(&msg, NULL, 0, 0, PM_REMOVE)) {
                if (msg.message == WM_QUIT) break;
                TranslateMessage(&msg);
                DispatchMessageW(&msg);
            }
            if (msg.message == WM_QUIT) break;
        }
        if (!IsWindow(state.hwnd)) { state.cancelled = true; break; }
    }

    // Extra pump for cookie retrieval
    if (state.loginDetected && !state.cancelled) {
        DWORD extraTimeout = GetTickCount() + 5000;
        while (GetTickCount() < extraTimeout && IsWindow(state.hwnd)) {
            MSG msg;
            while (PeekMessageW(&msg, NULL, 0, 0, PM_REMOVE)) {
                if (msg.message == WM_QUIT) break;
                TranslateMessage(&msg);
                DispatchMessageW(&msg);
            }
            if (!IsWindow(state.hwnd)) break;
            {
                std::lock_guard<std::mutex> lock(state.mtx);
                if (!state.resultJSON.empty()) break;
            }
            Sleep(50);
        }
    }

    // Cleanup
    if (state.controller) state.controller->Close();
    if (IsWindow(state.hwnd)) DestroyWindow(state.hwnd);
    UnregisterClassW(CLASS_NAME, hInst);

    if (!state.loginDetected && !state.cancelled) {
        return _strdup("{\"success\":false,\"error\":\"login timed out\",\"cookies\":[]}");
    }
    if (state.cancelled && !state.loginDetected) {
        return _strdup("{\"success\":false,\"error\":\"login cancelled\",\"cookies\":[]}");
    }
    if (!state.resultJSON.empty()) {
        return _strdup(state.resultJSON.c_str());
    }
    return _strdup("{\"success\":false,\"error\":\"no cookies extracted\",\"cookies\":[]}");
}

void freeStringC(char* str) {
    if (str) free(str);
}

} // extern "C"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"runtime"
	"unsafe"
)

// CookieResult holds the results of a WebView2 cookie extraction.
type CookieResult struct {
	Success bool        `json:"success"`
	Cookies []RawCookie `json:"cookies"`
	Error   string      `json:"error"`
}

// RawCookie is a cookie as returned by the WebView2 CookieManager.
type RawCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Secure   bool    `json:"secure"`
	HttpOnly bool    `json:"http_only"`
	SameSite int     `json:"same_site"`
}

// SameSite values returned by WebView2 CookieManager.
const (
	SameSiteNone   = 0
	SameSiteLax    = 1
	SameSiteStrict = 2
)

// ExtractCookies opens a WebView2 window, navigates to loginURL,
// waits for the user to complete CAS login and return to pan.ncepu.edu.cn,
// then extracts cookies via CookieManager.GetCookiesAsync.
func ExtractCookies(loginURL string, timeoutSeconds int) (*CookieResult, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cURL := C.CString(loginURL)
	defer C.free(unsafe.Pointer(cURL))

	cResult := C.extractCookiesC(cURL, C.int(timeoutSeconds))
	if cResult == nil {
		return nil, fmt.Errorf("webview2: null result from cookie extraction")
	}
	defer C.freeStringC(cResult)

	jsonStr := C.GoString(cResult)
	var result CookieResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("webview2: failed to parse result: %w", err)
	}
	return &result, nil
}
