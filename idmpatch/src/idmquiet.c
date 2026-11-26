/*
 * idmquiet.c - IDM 反篡改提示自动关闭 + 购买页拦截（双杀守护）
 *
 * 1) 自动关闭 IDM 的「cracked product」/「以管理员身份」提示框。
 * 2) 拦截 IDM 打开的购买页：凡父进程是 IDMan.exe 的浏览器进程
 *    (msedge/chrome/firefox/iexplore/opera/brave)，立即结束 —— 不让它跳浏览器。
 *
 * 编译(32位): gcc -O2 -municode -o idmquiet.exe idmquiet.c -luser32
 * 运行: idmquiet.exe        常驻(单实例)   |  idmquiet.exe -once  扫 20s
 */
#include <windows.h>
#include <tlhelp32.h>
#include <stdio.h>
#include <string.h>

static int g_idm[128]; static int g_n=0;
static int is_idm(DWORD pid){ for(int i=0;i<g_n;i++) if((DWORD)g_idm[i]==pid) return 1; return 0; }

static void act(void){
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0);
    if(snap==INVALID_HANDLE_VALUE) return;
    PROCESSENTRY32W pe; pe.dwSize=sizeof(pe);
    /* pass 1: collect idm pids */
    g_n=0;
    if(Process32FirstW(snap,&pe)){
        do{ if(_wcsicmp(pe.szExeFile,L"IDMan.exe")==0 && g_n<128) g_idm[g_n++]=(int)pe.th32ProcessID; }while(Process32NextW(snap,&pe));
    }
    /* pass 2: kill browser children of idm */
    if(Process32FirstW(snap,&pe)){
        do{
            const wchar_t *nm=pe.szExeFile;
            int isbrowser = _wcsicmp(nm,L"msedge.exe")==0||_wcsicmp(nm,L"chrome.exe")==0||
                            _wcsicmp(nm,L"firefox.exe")==0||_wcsicmp(nm,L"iexplore.exe")==0||
                            _wcsicmp(nm,L"opera.exe")==0||_wcsicmp(nm,L"brave.exe")==0;
            if(isbrowser && is_idm(pe.th32ParentProcessID)){
                HANDLE h=OpenProcess(PROCESS_TERMINATE,FALSE,pe.th32ProcessID);
                if(h){ TerminateProcess(h,0); CloseHandle(h); }
            }
        }while(Process32NextW(snap,&pe));
    }
    CloseHandle(snap);
}

static void get_text(HWND h,wchar_t*b,int n){ b[0]=0; SendMessageW(h,WM_GETTEXT,(WPARAM)n,(LPARAM)b); }
/* mode: 1=警告框(可点确定/OK/是), 2=注册框(只点取消/关闭, 绝不点确定) */
struct btnctx{int clicked;int mode;};
static BOOL CALLBACK enum_btn(HWND h,LPARAM lp){
    struct btnctx*c=(struct btnctx*)lp;
    wchar_t cls[32];GetClassNameW(h,cls,32);
    if(wcscmp(cls,L"Button")==0){wchar_t t[64];get_text(h,t,64);
        int hit=0;
        if(c->mode==2){
            /* 注册框：只允许取消/关闭，避免空表单点确定导致『请输入名称』死循环 */
            if(wcsstr(t,L"取消")||wcsstr(t,L"关闭")) hit=1;
        }else{
            if(wcsstr(t,L"确定")||wcsstr(t,L"OK")||wcsstr(t,L"是")||wcsstr(t,L"关闭")) hit=1;
        }
        if(hit){ PostMessageW(h,BM_CLICK,0,0); c->clicked=1; return FALSE; }
    }
    return TRUE;
}
static BOOL CALLBACK enum_top(HWND h,LPARAM lp){
    if(!IsWindowVisible(h))return TRUE;
    wchar_t cls[32];GetClassNameW(h,cls,32);
    if(wcscmp(cls,L"#32770")!=0)return TRUE;
    DWORD pid=0;GetWindowThreadProcessId(h,&pid);
    if(!is_idm(pid))return TRUE;
    wchar_t title[256];get_text(h,title,256);
    int is_warn = (wcscmp(title,L"Internet Download Manager")==0||wcscmp(title,L"IDM")==0);
    int is_reg  = (wcsstr(title,L"注册 Internet Download Manager")!=NULL);
    if(!is_warn && !is_reg) return TRUE;
    struct btnctx x={0}; x.mode = is_reg ? 2 : 1;
    EnumChildWindows(h,enum_btn,(LPARAM)&x);
    if(!x.clicked)PostMessageW(h,WM_CLOSE,0,0);
    return TRUE;
}

int WINAPI wWinMain(HINSTANCE hi,HINSTANCE hp,PWSTR cmdline,int show){
    (void)hi;(void)hp;(void)show;
    int once = (cmdline && wcsstr(cmdline,L"-once")) ? 1 : 0;
    HANDLE mtx=CreateMutexW(NULL,TRUE,L"Global\\idmquiet_single_instance_v1");
    if(mtx&&GetLastError()==ERROR_ALREADY_EXISTS&&!once)return 0;
    int loops=once?40:-1;
    for(int i=0;loops<0||i<loops;i++){
        act();
        EnumWindows(enum_top,0);
        Sleep(500);
    }
    if(mtx)CloseHandle(mtx);
    return 0;
}
