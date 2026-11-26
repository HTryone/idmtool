/*
 * idmtool.c - IDM 6.43b18 激活工具（GUI）· 除常驻守护 idmquiet.exe 外的全部功能
 *
 * 交付两个文件：
 *   1) idmtool.exe   —— 本 GUI：一键安装 / 还原 / 状态 / 一次性激活 / 生成序列号
 *   2) idmquiet.exe  —— 常驻守护（单独文件）：关提示框 + 阻止打开购买页
 *
 * 编译(32位):
 *   windres -i app.rc -o app_res.o
 *   gcc -O2 -municode -o idmtool.exe idmtool.c app_res.o -lcomctl32 -luser32 -ladvapi32 -lshell32
 * app.rc 内含: 1 24 "app.manifest"  (requireAdministrator 清单 + 图标可选)
 */
#include <windows.h>
#include <commctrl.h>
#include <tlhelp32.h>
#include <shellapi.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <fcntl.h>
#include <io.h>
#include <stdarg.h>

#define IDI_APPICON 1
static BOOL g_admin=FALSE;
static int is_admin_now(void){
    BOOL b=FALSE; PSID a; SID_IDENTIFIER_AUTHORITY nt=SECURITY_NT_AUTHORITY;
    if(AllocateAndInitializeSid(&nt,2,SECURITY_BUILTIN_DOMAIN_RID,DOMAIN_ALIAS_RID_ADMINS,0,0,0,0,0,0,&a)){
        if(!CheckTokenMembership(NULL,a,&b)) b=FALSE; FreeSid(a);} return b;
}
/* 若当前非管理员：用 runas 重新启动自身（退出当前实例） */
static void ensure_admin_or_relaunch(void){
    g_admin=is_admin_now();
    if(g_admin) return;
    wchar_t exe[MAX_PATH]; GetModuleFileNameW(NULL,exe,MAX_PATH);
    SHELLEXECUTEINFOW sei={0}; sei.cbSize=sizeof(sei); sei.lpVerb=L"runas"; sei.lpFile=exe; sei.nShow=SW_SHOWNORMAL;
    sei.fMask=0x00000040; /*SEE_MASK_NOCLOSEPROCESS*/
    if(ShellExecuteExW(&sei)){ ExitProcess(0); }
}

#define IDM_DIR   L"C:\\Program Files (x86)\\Internet Download Manager"
#define IDM_EXE   L"C:\\Program Files (x86)\\Internet Download Manager\\IDMan.exe"
#define REG_PATH  L"Software\\DownloadManager"
#define REG_RUN   L"Software\\Microsoft\\Windows\\CurrentVersion\\Run"
#define DLG_TITLE L"注册 Internet Download Manager"

/* 控件 ID */
#define IDC_LOG     1001
#define IDC_INSTALL 1002
#define IDC_UNINST  1003
#define IDC_STATUS  1004
#define IDC_ACTIVATE 1005
#define IDC_KEYGEN  1006

/* 序列号算法 */
static const char CHARSET[]="2YOPB3AQCVUXMNRS97WE0IZD4KLFGHJ8165T";
static const int MODS[4]={43,23,17,53};

/* 6 处补丁 */
typedef struct { unsigned off; const unsigned char*old; const unsigned char*neu; int len; const char*note; } PATCH;
static const unsigned char P1o[]={0xE8,0x21,0xE1,0xFF,0xFF}, P1n[]={0x31,0xC0,0x90,0x90,0x90};
static const unsigned char P2o[]={'T','r','i','a','l',0}, P2n[]={'F','u','l','l',' '};
static const unsigned char P3o[]={'T','r','i','a','l'}, P3n[]={'F','u','l','l',' '};
static const unsigned char P4o[]={'T','r','i','a','l',0}, P4n[]={'F','u','l','l',' '};
static const unsigned char P5o[]={0x01,0x00,0x00,0x00}, P5n[]={0x00,0x00,0x00,0x00};
static const unsigned char BUY_url[]="https://secure.internetdownloadmanager.com/buy.html";
static unsigned char ZEROZ[52];
static const PATCH PATCHES[]={
    {0x05190A,P1o,P1n,5,"skip license check"},
    {0x2D8A90,P2o,P2n,5,"Trial->Full (1)"},
    {0x2E30B3,P3o,P3n,5,"Trial->Full (%s)"},
    {0x2FBB85,P4o,P4n,5,"Trial->Full (3)"},
    {0x3C3724,P5o,P5n,4,"lic_flag=0"},
    {0x312DF8,BUY_url,ZEROZ,51,"blank buy.html"},
};
#define NPATCH (sizeof(PATCHES)/sizeof(PATCHES[0]))
static const char*BUY_TMPL[]={
    "%sbuy_idm.html?%s%s","%sbuy_idm.html?%s","%sbuy1.html?%s%s","%sbuy1.html?%s",
    "%sbuy.html?%s%s","%sbuy.html?%s","https://secure.internetdownloadmanager.com/buy1.html"
};

static HWND g_log;
static int  g_dpi=96;
static int  S(int v){ return MulDiv(v,g_dpi,96); }   /* 按 DPI 缩放 */

/* ===== 日志 ===== */
static void logf_(const wchar_t*fmt,...){
    wchar_t buf[2048]; va_list ap; va_start(ap,fmt); _vsnwprintf(buf,2048,fmt,ap); va_end(ap);
    int n=GetWindowTextLengthW(g_log);
    SendMessageW(g_log,EM_SETSEL,n,n);
    wchar_t line[2100]; _snwprintf(line,2100,L"%s\r\n",buf);
    SendMessageW(g_log,EM_REPLACESEL,FALSE,(LPARAM)line);
    SendMessageW(g_log,EM_SCROLLCARET,0,0);
}

/* ===== 文件/进程/注册表 ===== */
static int file_exists(const wchar_t*p){ return GetFileAttributesW(p)!=INVALID_FILE_ATTRIBUTES; }
static unsigned char*read_file(const wchar_t*p,unsigned*sz){
    FILE*f=_wfopen(p,L"rb"); if(!f)return NULL; fseek(f,0,SEEK_END); long n=ftell(f); fseek(f,0,SEEK_SET);
    unsigned char*d=(unsigned char*)malloc(n); if(!d){fclose(f);return NULL;}
    if(fread(d,1,n,f)!=(size_t)n){fclose(f);free(d);return NULL;} fclose(f);*sz=(unsigned)n;return d;
}
static int write_file(const wchar_t*p,unsigned char*d,unsigned sz){ FILE*f=_wfopen(p,L"wb"); if(!f)return 0; int ok=(fwrite(d,1,sz,f)==sz); fclose(f);return ok; }
static void exe_dir(wchar_t*out){ GetModuleFileNameW(NULL,out,MAX_PATH); wchar_t*p=wcsrchr(out,L'\\'); if(p)*p=0; }
static void kill_proc(const wchar_t*name){
    HANDLE s=CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0); if(s==INVALID_HANDLE_VALUE)return;
    PROCESSENTRY32W pe;pe.dwSize=sizeof(pe);
    if(Process32FirstW(s,&pe))do{ if(_wcsicmp(pe.szExeFile,name)==0){HANDLE h=OpenProcess(PROCESS_TERMINATE,FALSE,pe.th32ProcessID);if(h){TerminateProcess(h,0);CloseHandle(h);}}}while(Process32NextW(s,&pe));
    CloseHandle(s);
}
static int proc_running(const wchar_t*name){
    HANDLE s=CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0); if(s==INVALID_HANDLE_VALUE)return 0;
    PROCESSENTRY32W pe;pe.dwSize=sizeof(pe);int f=0;
    if(Process32FirstW(s,&pe))do{ if(_wcsicmp(pe.szExeFile,name)==0){f=1;break;}}while(Process32NextW(s,&pe));
    CloseHandle(s);return f;
}
static void set_reg_sz(HKEY root,const wchar_t*path,const wchar_t*name,const wchar_t*val){
    HKEY h; if(RegCreateKeyExW(root,path,0,NULL,0,KEY_SET_VALUE,NULL,&h,NULL)==ERROR_SUCCESS){
        RegSetValueExW(h,name,0,REG_SZ,(const BYTE*)val,(DWORD)((wcslen(val)+1)*sizeof(wchar_t)));RegCloseKey(h);}
}
static void del_reg(HKEY root,const wchar_t*path,const wchar_t*name){
    HKEY h; if(RegOpenKeyExW(root,path,0,KEY_SET_VALUE,&h)==ERROR_SUCCESS){RegDeleteValueW(h,name);RegCloseKey(h);}
}
static int reg_has(const wchar_t*path,const wchar_t*name){
    HKEY h; int has=0; if(RegOpenKeyExW(HKEY_CURRENT_USER,path,0,KEY_QUERY_VALUE,&h)==ERROR_SUCCESS){
        has=(RegQueryValueExW(h,name,0,NULL,NULL,NULL)==ERROR_SUCCESS); RegCloseKey(h);} return has;
}

/* ===== 序列号 ===== */
static int idx_ch(char c){ for(int i=0;CHARSET[i];i++)if(CHARSET[i]==c)return i; return -1; }
static void make_group(char*out,int mod){ for(;;){long long v=0;for(int i=0;i<5;i++){int j=rand()%36;out[i]=CHARSET[j];v=v*37+j;}out[5]=0;if(v%mod==0)return;} }
static void gen_serial(char*out){ char g[6];int pos=0;for(int k=0;k<4;k++){make_group(g,MODS[k]);memcpy(out+pos,g,5);pos+=5;if(k<3)out[pos++]='-';}out[pos]=0; }

/* ===== 补丁 ===== */
static int apply_patches(unsigned char*d,unsigned sz){
    if(sz<0x3C3800u){ logf_(L"[FAIL] 文件太小"); return 0; }
    for(unsigned i=0;i<NPATCH;i++) if(memcmp(d+PATCHES[i].off,PATCHES[i].old,PATCHES[i].len)!=0){
        logf_(L"[FAIL] %hs @0x%X 不匹配",PATCHES[i].note,PATCHES[i].off); return 0; }
    for(unsigned i=0;i<NPATCH;i++){ memcpy(d+PATCHES[i].off,PATCHES[i].neu,PATCHES[i].len); logf_(L"[ok] %hs",PATCHES[i].note); }
    for(unsigned t=0;t<sizeof(BUY_TMPL)/sizeof(BUY_TMPL[0]);t++){
        const char*pat=BUY_TMPL[t];int pl=(int)strlen(pat),cnt=0;
        for(unsigned p=0;p+pl<=sz;p++) if(memcmp(d+p,pat,pl)==0){memset(d+p,0,pl);cnt++;p+=pl-1;}
        logf_(L"[ok] 清空购买模板 [%hs] x%d",pat,cnt);
    }
    return 1;
}

/* ===== 安装/还原/状态/激活 ===== */
static void do_install(void){
    wchar_t dir[MAX_PATH]; exe_dir(dir);
    wchar_t backup[MAX_PATH]; wsprintfW(backup,L"%s\\IDMan.exe.orig-backup",dir);
    logf_(L"—— 一键安装 ——");
    kill_proc(L"IDMan.exe"); kill_proc(L"idmquiet.exe"); Sleep(2000);
    if(!file_exists(backup)){ if(!CopyFileW(IDM_EXE,backup,FALSE)){ logf_(L"[FAIL] 备份失败 err=%lu",GetLastError()); return; } logf_(L"[ok] 已备份原版"); }
    else logf_(L"[ok] 已有备份");
    unsigned sz; unsigned char*d=read_file(backup,&sz); if(!d){logf_(L"[FAIL] 读备份失败");return;}
    if(!apply_patches(d,sz)){ free(d); return; }
    if(!write_file(IDM_EXE,d,sz)){ free(d); logf_(L"[FAIL] 写 IDMan.exe 失败（管理员？）"); return; }
    free(d); logf_(L"[ok] 补丁版 IDMan.exe 已安装");
    set_reg_sz(HKEY_CURRENT_USER,REG_PATH,L"FName",L"HTryone");
    set_reg_sz(HKEY_CURRENT_USER,REG_PATH,L"LName",L"Hu");
    set_reg_sz(HKEY_CURRENT_USER,REG_PATH,L"Email",L"htryone@163.com");
    if(!reg_has(REG_PATH,L"Serial")){ char s[32];gen_serial(s);wchar_t ws[32];MultiByteToWideChar(CP_ACP,0,s,-1,ws,32);set_reg_sz(HKEY_CURRENT_USER,REG_PATH,L"Serial",ws); logf_(L"[ok] 序列号 %hs",s); }
    logf_(L"[ok] 注册信息：HTryone Hu");
    /* deploy idmquiet */
    wchar_t dstDir[MAX_PATH]; GetEnvironmentVariableW(L"LOCALAPPDATA",dstDir,MAX_PATH); wcscat(dstDir,L"\\idmfix"); CreateDirectoryW(dstDir,NULL);
    wchar_t src[MAX_PATH],dst[MAX_PATH]; wsprintfW(src,L"%s\\idmquiet.exe",dir); wsprintfW(dst,L"%s\\idmquiet.exe",dstDir);
    if(file_exists(src)){ CopyFileW(src,dst,FALSE); logf_(L"[ok] idmquiet 部署到 %ls",dst); }
    set_reg_sz(HKEY_CURRENT_USER,REG_RUN,L"idmquiet",dst);
    logf_(L"[ok] idmquiet 开机自启已设置");
    ShellExecuteW(NULL,L"open",dst,NULL,NULL,SW_HIDE);
    ShellExecuteW(NULL,L"open",IDM_EXE,NULL,NULL,SW_SHOWNORMAL);
    logf_(L"[DONE] 安装完成！关于框应显示 Full / 此产品授权给: HTryone Hu");
}
static void do_uninstall(void){
    wchar_t dir[MAX_PATH]; exe_dir(dir);
    wchar_t backup[MAX_PATH]; wsprintfW(backup,L"%s\\IDMan.exe.orig-backup",dir);
    logf_(L"—— 还原 ——");
    kill_proc(L"IDMan.exe"); kill_proc(L"idmquiet.exe"); Sleep(2000);
    if(file_exists(backup)){ if(CopyFileW(backup,IDM_EXE,FALSE)) logf_(L"[ok] 已还原原版 IDMan.exe"); else logf_(L"[FAIL] 还原失败 err=%lu",GetLastError()); }
    else logf_(L"[!] 未找到备份");
    del_reg(HKEY_CURRENT_USER,REG_RUN,L"idmquiet");
    logf_(L"[ok] 已删除 idmquiet 自启");
}
static void do_status(void){
    wchar_t dir[MAX_PATH]; exe_dir(dir);
    wchar_t backup[MAX_PATH]; wsprintfW(backup,L"%s\\IDMan.exe.orig-backup",dir);
    unsigned sz; unsigned char*d=read_file(IDM_EXE,&sz);
    logf_(L"—— 状态 ——");
    if(d){ int ok=1; for(unsigned i=0;i<NPATCH;i++) ok&=memcmp(d+PATCHES[i].off,PATCHES[i].neu,PATCHES[i].len)==0;
        logf_(L"IDMan.exe 补丁：%ls", ok?L"全部存在（已激活）":L"缺失/部分"); free(d); }
    else logf_(L"IDMan.exe：无法读取");
    logf_(L"原版备份：%ls", file_exists(backup)?L"存在":L"缺失");
    logf_(L"idmquiet：%ls", proc_running(L"idmquiet.exe")?L"运行中":L"未运行");
    logf_(L"开机自启：%ls", reg_has(REG_RUN,L"idmquiet")?L"已启用":L"未设置");
}
static void do_activate(void){
    logf_(L"—— 一次性激活（对话框）——");
    kill_proc(L"IDMan.exe"); Sleep(1500);
    del_reg(HKEY_CURRENT_USER,REG_PATH,L"FName"); del_reg(HKEY_CURRENT_USER,REG_PATH,L"LName");
    del_reg(HKEY_CURRENT_USER,REG_PATH,L"Email"); del_reg(HKEY_CURRENT_USER,REG_PATH,L"Serial");
    ShellExecuteW(NULL,L"open",IDM_EXE,NULL,NULL,SW_SHOWNORMAL);
    HWND dlg=NULL; for(int i=0;i<25&&!dlg;i++){ Sleep(1000); dlg=FindWindowW(NULL,DLG_TITLE); }
    if(!dlg){ logf_(L"[!] 25s 内未出现注册框（若已激活可忽略）"); return; }
    HWND edits[8];int ne=0;HWND ok=NULL; HWND c=GetWindow(dlg,GW_CHILD);
    while(c){ wchar_t cl[64],tx[128];GetClassNameW(c,cl,64);GetWindowTextW(c,tx,128);
        if(!wcscmp(cl,L"Edit")&&ne<8)edits[ne++]=c; else if(!wcscmp(cl,L"Button")&&wcsstr(tx,L"确定"))ok=c;
        c=GetWindow(c,GW_HWNDNEXT);}
    if(ne<4||!ok){ logf_(L"[!] 控件不足"); return; }
    char s[32];gen_serial(s);wchar_t ws[32];MultiByteToWideChar(CP_ACP,0,s,-1,ws,32);
    SendMessageW(edits[0],WM_SETTEXT,0,(LPARAM)L"HTryone");SendMessageW(edits[1],WM_SETTEXT,0,(LPARAM)L"Hu");
    SendMessageW(edits[2],WM_SETTEXT,0,(LPARAM)L"htryone@163.com");SendMessageW(edits[3],WM_SETTEXT,0,(LPARAM)ws);
    Sleep(200);SendMessageW(ok,BM_CLICK,0,0);
    logf_(L"[DONE] 已激活：HTryone Hu | %hs",s);
}
static void do_keygen(void){
    char s[32]; gen_serial(s);
    logf_(L"[序列号] %hs",s);
}
/* 后台线程执行耗时操作，避免卡 UI */
static DWORD WINAPI worker(LPVOID p){
    int cmd=(int)(INT_PTR)p;
    switch(cmd){
        case IDC_INSTALL: do_install(); break;
        case IDC_UNINST:  do_uninstall(); break;
        case IDC_STATUS:  do_status(); break;
        case IDC_ACTIVATE:do_activate(); break;
        case IDC_KEYGEN:  do_keygen(); break;
    }
    EnableWindow(GetDlgItem(GetActiveWindow(),cmd),TRUE);
    return 0;
}
static void run_async(HWND h,int cmd){ EnableWindow(GetDlgItem(h,cmd),FALSE); CloseHandle(CreateThread(NULL,0,worker,(LPVOID)(INT_PTR)cmd,0,NULL)); }

/* ===== 主窗口 ===== */
static LRESULT CALLBACK WndProc(HWND h,UINT msg,WPARAM wp,LPARAM lp){
    static HFONT font;
    switch(msg){
    case WM_CREATE:{
        /* 微软雅黑 UI，按 DPI 设定字号 */
        int pt = 10; /* 10pt */
        font=CreateFontW(-MulDiv(pt,g_dpi,72),0,0,0,FW_NORMAL,0,0,0,DEFAULT_CHARSET,
              OUT_TT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH|FF_DONTCARE,L"微软雅黑");
        if(!font) font=(HFONT)GetStockObject(DEFAULT_GUI_FONT);
        int x=S(12),y=S(12),bw=S(148),bh=S(34),gap=S(10);
        int logX=x+bw+S(12), logW=S(400), logH=S(340);
        struct {int id; const wchar_t*t;} btns[]={
            {IDC_INSTALL,L"一键安装 / 激活"},{IDC_UNINST,L"还原原版"},
            {IDC_STATUS,L"查看状态"},{IDC_ACTIVATE,L"一次性激活（对话框）"},{IDC_KEYGEN,L"生成序列号"}};
        int yy=y;
        for(int i=0;i<5;i++){ HWND b=CreateWindowW(L"BUTTON",btns[i].t,WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON|BS_MULTILINE,
            x,yy,bw,bh,h,(HMENU)(INT_PTR)btns[i].id,NULL,NULL); SendMessageW(b,WM_SETFONT,(WPARAM)font,TRUE); yy+=bh+gap; }
        g_log=CreateWindowExW(WS_EX_CLIENTEDGE,L"EDIT",L"",WS_CHILD|WS_VISIBLE|WS_VSCROLL|ES_MULTILINE|ES_READONLY|ES_AUTOVSCROLL,
            logX,y,logW,logH,h,(HMENU)IDC_LOG,NULL,NULL);
        SendMessageW(g_log,WM_SETFONT,(WPARAM)font,TRUE);
        logf_(L"IDM 6.43b18 激活工具");
        logf_(L"点「一键安装 / 激活」即可（需管理员，已自动请求）。");
        logf_(L"把 idmquiet.exe 与本程序放在同一目录，安装时会自动部署守护。");
        return 0; }
    case WM_COMMAND:
        switch(LOWORD(wp)){
            case IDC_INSTALL: case IDC_UNINST: case IDC_STATUS: case IDC_ACTIVATE: case IDC_KEYGEN:
                run_async(h,LOWORD(wp)); return 0;
        }
        break;
    case WM_DESTROY: PostQuitMessage(0); return 0;
    }
    return DefWindowProcW(h,msg,wp,lp);
}
int WINAPI wWinMain(HINSTANCE hi,HINSTANCE hp,PWSTR cmd,int show){
    for(int i=0;i<52;i++) ZEROZ[i]=0;
    srand((unsigned)(time(NULL)^GetCurrentProcessId()));
    ensure_admin_or_relaunch();
    /* 高 DPI 感知：运行期动态调用 Per-Monitor V2，失败退回 SetProcessDPIAware */
    {
        typedef BOOL (WINAPI *PFN_SPDAC)(HANDLE);
        HMODULE u32=GetModuleHandleW(L"user32.dll");
        PFN_SPDAC spdac=u32?(PFN_SPDAC)GetProcAddress(u32,"SetProcessDpiAwarenessContext"):NULL;
        if(!(spdac && spdac((HANDLE)-4))) SetProcessDPIAware();
    }
    {   HDC hdc=GetDC(NULL); if(hdc){ g_dpi=GetDeviceCaps(hdc,LOGPIXELSX); ReleaseDC(NULL,hdc);} if(g_dpi<96)g_dpi=96; }
    InitCommonControls();
    WNDCLASSW wc={0}; wc.lpfnWndProc=WndProc; wc.hInstance=hi; wc.lpszClassName=L"IDMFixToolWnd";
    wc.hCursor=LoadCursor(NULL,IDC_ARROW); wc.hbrBackground=(HBRUSH)(COLOR_BTNFACE+1);
    wc.hIcon=LoadIconW(hi,MAKEINTRESOURCEW(IDI_APPICON));
    RegisterClassW(&wc);
    RECT rc={0,0,600,400};
    AdjustWindowRectEx(&rc,WS_OVERLAPPEDWINDOW^WS_THICKFRAME^WS_MAXIMIZEBOX,FALSE,0);
    HWND h=CreateWindowExW(0,L"IDMFixToolWnd",L"IDM 6.43b18 激活工具",WS_OVERLAPPEDWINDOW^WS_THICKFRAME^WS_MAXIMIZEBOX,
        CW_USEDEFAULT,CW_USEDEFAULT,S(rc.right-rc.left),S(rc.bottom-rc.top),NULL,NULL,hi,NULL);
    SendMessageW(h,WM_SETICON,ICON_BIG,(LPARAM)LoadIconW(hi,MAKEINTRESOURCEW(IDI_APPICON)));
    SendMessageW(h,WM_SETICON,ICON_SMALL,(LPARAM)LoadIconW(hi,MAKEINTRESOURCEW(IDI_APPICON)));
    ShowWindow(h,show); UpdateWindow(h);
    MSG m; while(GetMessageW(&m,NULL,0,0)){ if(!IsDialogMessageW(h,&m)){ TranslateMessage(&m); DispatchMessageW(&m);} }
    return 0;
}
