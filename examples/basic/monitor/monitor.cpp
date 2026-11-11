#define _WIN32_WINNT 0x0A00
#define WIN32_LEAN_AND_MEAN

#include <windows.h>
#include <sddl.h>
#include <tlhelp32.h>

#include <cstdio>
#include <cstdlib>
#include <cwchar>
#include <exception>
#include <filesystem>
#include <fstream>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

namespace {

const std::filesystem::path kDataDir = L"C:\\vsystemd-example\\data";
const std::filesystem::path kLogPath = kDataDir / L"file1.txt";

std::mutex g_logMutex;
std::ofstream g_log;
HANDLE g_exitEvent = nullptr;

std::string Narrow(const std::wstring& value) {
    if (value.empty()) {
        return {};
    }
    const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()),
                                         nullptr, 0, nullptr, nullptr);
    if (size <= 0) {
        return {};
    }
    std::string result(static_cast<size_t>(size), '\0');
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()),
                        result.data(), size, nullptr, nullptr);
    return result;
}

std::wstring Timestamp() {
    SYSTEMTIME time;
    GetLocalTime(&time);
    wchar_t buffer[64];
    swprintf_s(buffer, L"%04u-%02u-%02u %02u:%02u:%02u.%03u",
               time.wYear, time.wMonth, time.wDay,
               time.wHour, time.wMinute, time.wSecond, time.wMilliseconds);
    return buffer;
}

std::wstring FormatWin32Error(DWORD error) {
    wchar_t* message = nullptr;
    const DWORD length = FormatMessageW(
        FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
        nullptr, error, 0, reinterpret_cast<LPWSTR>(&message), 0, nullptr);

    std::wstring text = length && message ? std::wstring(message, length) : L"unknown error";
    if (message) {
        LocalFree(message);
    }
    while (!text.empty() && (text.back() == L'\r' || text.back() == L'\n')) {
        text.pop_back();
    }
    return text + L" (error " + std::to_wstring(error) + L")";
}

void Log(const std::wstring& level, const std::wstring& message) {
    std::lock_guard<std::mutex> lock(g_logMutex);
    if (!g_log.is_open()) {
        g_log.open(kLogPath, std::ios::app | std::ios::binary);
    }
    if (!g_log.is_open()) {
        return;
    }

    const std::wstring line = Timestamp() + L" [" + level + L"] " + message + L"\n";
    const std::string utf8 = Narrow(line);
    g_log.write(utf8.data(), static_cast<std::streamsize>(utf8.size()));
    g_log.flush();
}

void LogInfo(const std::wstring& message) {
    Log(L"INFO", message);
}

void LogError(const std::wstring& message) {
    Log(L"ERROR", message);
}

void LogLastError(const std::wstring& label, DWORD error) {
    LogError(label + L": " + FormatWin32Error(error));
}

void LogValue(const std::wstring& label, const std::wstring& value) {
    LogInfo(label + L": " + value);
}

void LogValue(const std::wstring& label, DWORD value) {
    LogInfo(label + L": " + std::to_wstring(value));
}

void LogValueHex(const std::wstring& label, uintptr_t value) {
    wchar_t buffer[32];
    swprintf_s(buffer, L"0x%llX", static_cast<unsigned long long>(value));
    LogInfo(label + L": " + buffer);
}

std::wstring SidToString(PSID sid) {
    wchar_t* text = nullptr;
    if (!ConvertSidToStringSidW(sid, &text)) {
        return L"<ConvertSidToStringSidW failed: " + FormatWin32Error(GetLastError()) + L">";
    }
    std::wstring result = text;
    LocalFree(text);
    return result;
}

std::wstring HandleType(HANDLE handle) {
    if (handle == nullptr || handle == INVALID_HANDLE_VALUE) {
        return L"invalid handle";
    }

    switch (GetFileType(handle)) {
        case FILE_TYPE_CHAR:
            return L"FILE_TYPE_CHAR";
        case FILE_TYPE_DISK:
            return L"FILE_TYPE_DISK";
        case FILE_TYPE_PIPE:
            return L"FILE_TYPE_PIPE";
        case FILE_TYPE_REMOTE:
            return L"FILE_TYPE_REMOTE";
        case FILE_TYPE_UNKNOWN:
        default:
            return L"FILE_TYPE_UNKNOWN: " + FormatWin32Error(GetLastError());
    }
}

DWORD ParentProcessId(DWORD pid) {
    HANDLE snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snapshot == INVALID_HANDLE_VALUE) {
        return 0;
    }

    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    DWORD parent = 0;
    if (Process32FirstW(snapshot, &entry)) {
        do {
            if (entry.th32ProcessID == pid) {
                parent = entry.th32ParentProcessID;
                break;
            }
        } while (Process32NextW(snapshot, &entry));
    }
    CloseHandle(snapshot);
    return parent;
}

void DumpTokenInfo() {
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) {
        LogLastError(L"OpenProcessToken", GetLastError());
        return;
    }

    DWORD required = 0;
    GetTokenInformation(token, TokenUser, nullptr, 0, &required);
    if (required > 0) {
        std::vector<BYTE> buffer(required);
        if (GetTokenInformation(token, TokenUser, buffer.data(), required, &required)) {
            auto* user = reinterpret_cast<TOKEN_USER*>(buffer.data());
            LogValue(L"Token user SID", SidToString(user->User.Sid));

            wchar_t name[256] = {};
            wchar_t domain[256] = {};
            DWORD nameLength = _countof(name);
            DWORD domainLength = _countof(domain);
            SID_NAME_USE use = SidTypeUnknown;
            if (LookupAccountSidW(nullptr, user->User.Sid, name, &nameLength, domain, &domainLength, &use)) {
                LogValue(L"Token user", std::wstring(domain) + L"\\" + name);
            } else {
                LogLastError(L"LookupAccountSidW", GetLastError());
            }
        } else {
            LogLastError(L"GetTokenInformation(TokenUser)", GetLastError());
        }
    }

    required = 0;
    GetTokenInformation(token, TokenIntegrityLevel, nullptr, 0, &required);
    if (required > 0) {
        std::vector<BYTE> buffer(required);
        if (GetTokenInformation(token, TokenIntegrityLevel, buffer.data(), required, &required)) {
            auto* label = reinterpret_cast<TOKEN_MANDATORY_LABEL*>(buffer.data());
            LogValue(L"Token integrity SID", SidToString(label->Label.Sid));
        } else {
            LogLastError(L"GetTokenInformation(TokenIntegrityLevel)", GetLastError());
        }
    }

    DWORD session = 0;
    required = 0;
    if (GetTokenInformation(token, TokenSessionId, &session, sizeof(session), &required)) {
        LogValue(L"Token session id", session);
    } else {
        LogLastError(L"GetTokenInformation(TokenSessionId)", GetLastError());
    }

    CloseHandle(token);
}

void DumpProcessInfo() {
    const DWORD pid = GetCurrentProcessId();
    const DWORD tid = GetCurrentThreadId();

    LogInfo(L"=== process dump ===");
    LogValue(L"PID", pid);
    LogValue(L"TID", tid);
    LogValue(L"Parent PID", ParentProcessId(pid));

    DWORD session = 0;
    if (ProcessIdToSessionId(pid, &session)) {
        LogValue(L"Session id", session);
    } else {
        LogLastError(L"ProcessIdToSessionId", GetLastError());
    }

    std::vector<wchar_t> modulePath(32768);
    const DWORD moduleLength = GetModuleFileNameW(nullptr, modulePath.data(), static_cast<DWORD>(modulePath.size()));
    LogValue(L"Module path", std::wstring(modulePath.data(), moduleLength));

    std::vector<wchar_t> cwd(32768);
    const DWORD cwdLength = GetCurrentDirectoryW(static_cast<DWORD>(cwd.size()), cwd.data());
    LogValue(L"Current directory", std::wstring(cwd.data(), cwdLength));

    LogValue(L"Command line", GetCommandLineW());

    DumpTokenInfo();

    HWINSTA station = GetProcessWindowStation();
    wchar_t stationName[256] = {};
    DWORD needed = 0;
    if (station && GetUserObjectInformationW(station, UOI_NAME, stationName, sizeof(stationName), &needed)) {
        LogValue(L"Window station", stationName);
    } else {
        LogLastError(L"GetUserObjectInformationW(window station)", GetLastError());
    }

    HDESK desktop = GetThreadDesktop(tid);
    wchar_t desktopName[256] = {};
    if (desktop && GetUserObjectInformationW(desktop, UOI_NAME, desktopName, sizeof(desktopName), &needed)) {
        LogValue(L"Desktop", desktopName);
    } else {
        LogLastError(L"GetUserObjectInformationW(desktop)", GetLastError());
    }

    for (const wchar_t* name : {L"USERNAME", L"USERDOMAIN", L"SESSIONNAME", L"USERPROFILE", L"TEMP"}) {
        wchar_t value[1024] = {};
        const DWORD length = GetEnvironmentVariableW(name, value, _countof(value));
        if (length == 0) {
            LogValue(std::wstring(L"Env ") + name, L"<not set>");
        } else if (length < _countof(value)) {
            LogValue(std::wstring(L"Env ") + name, value);
        } else {
            LogValue(std::wstring(L"Env ") + name, L"<too long>");
        }
    }

    LPWCH environment = GetEnvironmentStringsW();
    if (environment) {
        for (LPWCH entry = environment; *entry; entry += wcslen(entry) + 1) {
            if (wcsncmp(entry, L"VSYSTEMD_", 9) == 0) {
                LogValue(L"Env VSYSTEMD", entry);
            }
        }
        FreeEnvironmentStringsW(environment);
    }
}

void DumpWindow(HWND window) {
    wchar_t className[256] = {};
    wchar_t title[1024] = {};
    GetClassNameW(window, className, _countof(className));
    GetWindowTextW(window, title, _countof(title));

    RECT rect{};
    GetWindowRect(window, &rect);

    wchar_t text[1024];
    swprintf_s(text,
               L"window hwnd=0x%llX visible=%s class=\"%s\" title=\"%s\" rect=(%ld,%ld)-(%ld,%ld)",
               static_cast<unsigned long long>(reinterpret_cast<uintptr_t>(window)),
               IsWindowVisible(window) ? L"true" : L"false",
               className, title, rect.left, rect.top, rect.right, rect.bottom);
    LogInfo(text);
}

BOOL CALLBACK EnumWindowCallback(HWND window, LPARAM) {
    DWORD pid = 0;
    GetWindowThreadProcessId(window, &pid);
    if (pid == GetCurrentProcessId()) {
        DumpWindow(window);
    }
    return TRUE;
}

void DumpConsoleInfo() {
    LogInfo(L"=== console dump ===");

    HWND consoleWindow = GetConsoleWindow();
    LogValueHex(L"GetConsoleWindow", reinterpret_cast<uintptr_t>(consoleWindow));
    if (consoleWindow) {
        LogValue(L"IsWindowVisible(console)", IsWindowVisible(consoleWindow) ? L"true" : L"false");
        RECT rect{};
        if (GetWindowRect(consoleWindow, &rect)) {
            wchar_t text[256];
            swprintf_s(text, L"console rect=(%ld,%ld)-(%ld,%ld)",
                       rect.left, rect.top, rect.right, rect.bottom);
            LogInfo(text);
        } else {
            LogLastError(L"GetWindowRect(console)", GetLastError());
        }
        DumpWindow(consoleWindow);
    }

    LogValue(L"GetConsoleCP", GetConsoleCP());
    LogValue(L"GetConsoleOutputCP", GetConsoleOutputCP());

    wchar_t title[1024] = {};
    if (GetConsoleTitleW(title, _countof(title))) {
        LogValue(L"Console title", title);
    } else {
        LogLastError(L"GetConsoleTitleW", GetLastError());
    }

    const std::pair<const wchar_t*, DWORD> handles[] = {
        {L"STD_INPUT_HANDLE", STD_INPUT_HANDLE},
        {L"STD_OUTPUT_HANDLE", STD_OUTPUT_HANDLE},
        {L"STD_ERROR_HANDLE", STD_ERROR_HANDLE},
    };
    for (const auto& item : handles) {
        HANDLE handle = GetStdHandle(item.second);
        LogValue(std::wstring(L"Std handle ") + item.first, HandleType(handle));

        DWORD mode = 0;
        if (GetConsoleMode(handle, &mode)) {
            wchar_t text[64];
            swprintf_s(text, L"console mode 0x%lX", mode);
            LogValue(std::wstring(L"Std console mode ") + item.first, text);
        } else {
            LogValue(std::wstring(L"Std console mode ") + item.first,
                     L"<not a console handle: " + FormatWin32Error(GetLastError()) + L">");
        }
    }

    CONSOLE_SCREEN_BUFFER_INFO info{};
    if (GetConsoleScreenBufferInfo(GetStdHandle(STD_OUTPUT_HANDLE), &info)) {
        wchar_t text[256];
        swprintf_s(text, L"buffer=%ldx%ld cursor=(%d,%d) window=(%d,%d)-(%d,%d) attrs=0x%X",
                   info.dwSize.X, info.dwSize.Y,
                   info.dwCursorPosition.X, info.dwCursorPosition.Y,
                   info.srWindow.Left, info.srWindow.Top,
                   info.srWindow.Right, info.srWindow.Bottom,
                   info.wAttributes);
        LogInfo(text);
    } else {
        LogLastError(L"GetConsoleScreenBufferInfo", GetLastError());
    }

    DWORD processes[256] = {};
    const DWORD count = GetConsoleProcessList(processes, _countof(processes));
    if (count == 0) {
        LogLastError(L"GetConsoleProcessList", GetLastError());
    } else if (count > _countof(processes)) {
        LogValue(L"GetConsoleProcessList required", count);
    } else {
        std::wstring list;
        for (DWORD i = 0; i < count; ++i) {
            if (!list.empty()) {
                list += L", ";
            }
            list += std::to_wstring(processes[i]);
        }
        LogValue(L"GetConsoleProcessList", list);
    }

    LogInfo(L"=== windows owned by process ===");
    EnumWindows(EnumWindowCallback, 0);
}

const wchar_t* ControlEventName(DWORD event) {
    switch (event) {
        case CTRL_C_EVENT: return L"CTRL_C_EVENT";
        case CTRL_BREAK_EVENT: return L"CTRL_BREAK_EVENT";
        case CTRL_CLOSE_EVENT: return L"CTRL_CLOSE_EVENT";
        case CTRL_LOGOFF_EVENT: return L"CTRL_LOGOFF_EVENT";
        case CTRL_SHUTDOWN_EVENT: return L"CTRL_SHUTDOWN_EVENT";
        default: return L"UNKNOWN";
    }
}

BOOL WINAPI ConsoleControlHandler(DWORD event) {
    const std::wstring text = std::wstring(L"console control event ") + ControlEventName(event) +
                              L" (" + std::to_wstring(event) + L")";
    Log(L"EVENT", text);

    if (g_exitEvent) {
        SetEvent(g_exitEvent);
    }
    return TRUE;
}

LONG WINAPI UnhandledExceptionLogger(EXCEPTION_POINTERS* info) {
    wchar_t text[256];
    if (info && info->ExceptionRecord) {
        swprintf_s(text, L"unhandled exception code=0x%lX address=0x%llX",
                   info->ExceptionRecord->ExceptionCode,
                   static_cast<unsigned long long>(
                       reinterpret_cast<uintptr_t>(info->ExceptionRecord->ExceptionAddress)));
        LogError(text);
    } else {
        LogError(L"unhandled exception with no exception record");
    }
    return EXCEPTION_EXECUTE_HANDLER;
}

}  // namespace

int main() {
    std::error_code filesystemError;
    std::filesystem::create_directories(kDataDir, filesystemError);
    if (filesystemError) {
        LogError(L"create_directories failed, code=" + std::to_wstring(filesystemError.value()));
    }

    SetUnhandledExceptionFilter(UnhandledExceptionLogger);
    std::set_terminate([]() {
        LogError(L"std::terminate called");
        std::abort();
    });

    g_exitEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (!g_exitEvent) {
        LogLastError(L"CreateEventW", GetLastError());
        return 1;
    }

    LogInfo(L"=== monitor start ===");

    if (SetConsoleCtrlHandler(ConsoleControlHandler, TRUE)) {
        LogInfo(L"SetConsoleCtrlHandler installed");
    } else {
        LogLastError(L"SetConsoleCtrlHandler", GetLastError());
    }

    DumpProcessInfo();
    DumpConsoleInfo();

    for (;;) {
        Log(L"TICK", L"alive");
        const DWORD wait = WaitForSingleObject(g_exitEvent, 10000);
        if (wait == WAIT_OBJECT_0) {
            LogInfo(L"exit event received, stopping main loop");
            break;
        }
        if (wait == WAIT_FAILED) {
            LogLastError(L"WaitForSingleObject", GetLastError());
            break;
        }
    }

    LogInfo(L"=== monitor stop ===");
    if (g_exitEvent) {
        CloseHandle(g_exitEvent);
        g_exitEvent = nullptr;
    }
    return 0;
}