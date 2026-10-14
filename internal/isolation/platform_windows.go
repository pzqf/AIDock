//go:build windows

package isolation

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"golang.org/x/sys/windows"
)

var userenv = windows.NewLazySystemDLL("userenv.dll")
var createProfile = userenv.NewProc("CreateAppContainerProfile")
var deleteProfile = userenv.NewProc("DeleteAppContainerProfile")

const securityCapabilitiesAttribute = 0x20009
const childProcessAttribute = 0x2000e
const tokenIsAppContainer = 29

type securityCapabilities struct {
	SID             *windows.SID
	Capabilities    unsafe.Pointer
	Count, Reserved uint32
}

func platformInfo() Platform {
	v := windows.RtlGetVersion()
	host, _ := os.Hostname()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err == nil {
		host += "/" + u.User.Sid.String()
	}
	return Platform{Kernel: fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber), Host: workstore.Hash([]byte(host)), APIs: err == nil && createProfile.Find() == nil && deleteProfile.Find() == nil}
}
func checkProbePath(path string) error { return workstore.CheckLinks(path) }
func appContainerToken() bool          { return tokenContainer(windows.GetCurrentProcessToken()) }
func tokenContainer(token windows.Token) bool {
	var value, size uint32
	return windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&value)), 4, &size) == nil && value == 1
}
func denied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.Errno(10013))
}
func childDenied(err error) bool { return errors.Is(err, windows.ERROR_CHILD_PROCESS_BLOCKED) }

// protect grants one ephemeral SID on NEW fixture paths only. It never grants
// access to project roots, Local Root ancestors, or a third-party executable.
func protect(path, sid, access string) error {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sddl := "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if sid != "" {
		sddl += "(A;OICI;" + access + ";;;" + sid + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl + "S:(ML;OICI;NW;;;LW)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return err
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl)
}
func profile(profileName string) (*windows.SID, func() error, error) {
	name, _ := windows.UTF16PtrFromString(profileName)
	description, _ := windows.UTF16PtrFromString("AIDock temporary isolation test")
	var sid *windows.SID
	hr, _, _ := createProfile.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(description)), uintptr(unsafe.Pointer(description)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if int32(hr) < 0 {
		return nil, nil, fmt.Errorf("CreateAppContainerProfile HRESULT 0x%x", hr)
	}
	return sid, func() error {
		defer windows.FreeSid(sid)
		return removeContainer(profileName)
	}, nil
}

func removeContainer(name string) error {
	p, _ := windows.UTF16PtrFromString(name)
	hr, _, _ := deleteProfile.Call(uintptr(unsafe.Pointer(p)))
	if int32(hr) < 0 && uint32(hr) != 0x80070002 && uint32(hr) != 0x80070003 {
		return fmt.Errorf("DeleteAppContainerProfile HRESULT 0x%x", hr)
	}
	return nil
}
func lockPlatform(local string, fn func() error) error {
	if err := workstore.CheckLinks(local); err != nil {
		return err
	}
	if err := os.MkdirAll(local, 0700); err != nil {
		return err
	}
	s := &workstore.Store{Root: filepath.Join(local, "isolation-probes"), Local: filepath.Join(local, "isolation-state")}
	return s.Locked(fn)
}
func recoverPlatform(p Pending) error {
	// Reparse points may be created only at our fixed test-junction path.
	// Unexpected links or top-level files are retained for manual inspection.
	link := filepath.Join(p.Root, "stage", "input", "outside")
	allowed := map[string]bool{".aidock-isolation.json": true, "stage": true, "project": true, "private": true}
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !allowed[e.Name()] {
			return fmt.Errorf("恢复目录有额外内容，保留原目录：%s", filepath.Join(p.Root, e.Name()))
		}
	}
	err = filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == link {
			return filepath.SkipDir
		}
		return workstore.CheckLinks(path)
	})
	if err != nil {
		return err
	}
	// Delete the profile before the durable marker, so a retry still knows
	// which profile belongs to this fixture if filesystem cleanup fails.
	if err = removeContainer(p.Profile); err != nil {
		return err
	}
	if _, err = os.Lstat(link); err == nil {
		ptr, _ := windows.UTF16PtrFromString(link)
		if err = windows.RemoveDirectory(ptr); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = removeFixture(p); err != nil {
		return err
	}
	return nil
}

func removeFixture(p Pending) error {
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".aidock-isolation.json" {
			continue
		}
		if err = os.RemoveAll(filepath.Join(p.Root, e.Name())); err != nil {
			return err
		}
	}
	marker := filepath.Join(p.Root, ".aidock-isolation.json")
	if err = os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(p.Root); err != nil {
		_ = writePending(p)
		return err
	}
	return nil
}

// Used only by our interruption test executable, never by CLI/API callers.
var afterResume func()

// launch attaches OS restrictions before the suspended primary thread can run.
// No handles or user environment are inherited, no network capabilities, no
// breakaway, and child creation is explicitly forbidden.
func launch(ctx context.Context, stage string, sid *windows.SID, mode string, deadline time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.ProcessMemoryLimit = 256 << 20
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return false, err
	}
	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return false, err
	}
	defer attrs.Delete()
	caps := securityCapabilities{SID: sid}
	childPolicy := uint32(1) // PROCESS_CREATION_CHILD_PROCESS_RESTRICTED
	if err = attrs.Update(securityCapabilitiesAttribute, unsafe.Pointer(&caps), unsafe.Sizeof(caps)); err != nil {
		return false, err
	}
	if err = attrs.Update(childProcessAttribute, unsafe.Pointer(&childPolicy), unsafe.Sizeof(childPolicy)); err != nil {
		return false, err
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: attrs.List()}
	si.Desktop, _ = windows.UTF16PtrFromString(`winsta0\default`)
	exe := filepath.Join(stage, "bin", "probe.exe")
	exe16, _ := windows.UTF16PtrFromString(exe)
	command, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{exe, "__aidock_isolation_probe", mode}))
	cwd := filepath.Join(stage, "scratch")
	cwd16, _ := windows.UTF16PtrFromString(cwd)
	// Encode a double-terminated block; inherit no user secrets or handles.
	var environment []uint16
	for _, entry := range []string{"APPDATA=" + os.Getenv("APPDATA"), "GOMAXPROCS=2", "LOCALAPPDATA=" + os.Getenv("LOCALAPPDATA"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + cwd, "TMP=" + cwd, "USERPROFILE=" + os.Getenv("USERPROFILE"), "windir=" + os.Getenv("SystemRoot")} {
		part, e := windows.UTF16FromString(entry)
		if e != nil {
			return false, e
		}
		environment = append(environment, part...)
	}
	environment = append(environment, 0)
	var pi windows.ProcessInformation
	err = windows.CreateProcess(exe16, command, nil, nil, false, windows.CREATE_SUSPENDED|windows.DETACHED_PROCESS|windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT, &environment[0], cwd16, &si.StartupInfo, &pi)
	runtime.KeepAlive(caps)
	runtime.KeepAlive(childPolicy)
	runtime.KeepAlive(environment)
	if err != nil {
		return false, fmt.Errorf("CreateProcess AppContainer: %w", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	// Also kill a suspended process if job assignment or token checking fails.
	defer func() {
		_ = windows.TerminateProcess(pi.Process, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, 3000)
	}()
	if err = windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		return false, err
	}
	defer windows.TerminateJobObject(job, 1)
	var token windows.Token
	if err = windows.OpenProcessToken(pi.Process, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	container := tokenContainer(token)
	token.Close()
	if !container {
		return false, fmt.Errorf("created process is not AppContainer; refused before resume")
	}
	if _, err = windows.ResumeThread(pi.Thread); err != nil {
		return false, err
	}
	if afterResume != nil {
		afterResume()
	}
	timeout := time.Now().Add(deadline)
	var stopped error
	for {
		state, e := windows.WaitForSingleObject(pi.Process, 25)
		if e != nil {
			stopped = e
			break
		}
		if state == windows.WAIT_OBJECT_0 {
			var code uint32
			if e = windows.GetExitCodeProcess(pi.Process, &code); e != nil {
				stopped = e
			} else if code != 0 {
				stopped = fmt.Errorf("fixed probe exited %d", code)
			}
			break
		}
		if e = ctx.Err(); e != nil {
			stopped = e
			break
		}
		if time.Now().After(timeout) {
			stopped = context.DeadlineExceeded
			break
		}
	}
	if err = windows.TerminateJobObject(job, 1); err != nil {
		return false, err
	}
	state, e := windows.WaitForSingleObject(pi.Process, 3000)
	if e != nil || state != windows.WAIT_OBJECT_0 {
		return false, fmt.Errorf("job process cleanup not verified: %v", e)
	}
	return true, stopped
}

func junction(path, target string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	sub, _ := windows.UTF16FromString(`\??\` + target)
	printName, _ := windows.UTF16FromString(target)
	buf := make([]byte, 16+2*(len(sub)+len(printName)))
	binary.LittleEndian.PutUint32(buf[:4], 0xa0000003)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:12], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(2*(len(printName)-1)))
	for i, v := range append(sub, printName...) {
		binary.LittleEndian.PutUint16(buf[16+2*i:18+2*i], v)
	}
	var returned uint32
	return windows.DeviceIoControl(h, 0x900a4, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
}

func probePlatform(ctx context.Context, local string, p Platform) (findings []Finding, cleaned bool) {
	err := lockPlatform(local, func() error {
		m := &Manager{Local: local}
		pending, err := m.Pending()
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return result.Fail("ISOLATION_RECOVERY_REQUIRED", "先清理上次中断的隔离测试；不启动新进程", 6)
		}
		findings, cleaned = probeOnce(ctx, local, p)
		return nil
	})
	if err != nil {
		return []Finding{{"ISOLATION_RECOVERY_REQUIRED", "FAIL", err.Error()}}, true
	}
	return
}
func probeOnce(ctx context.Context, local string, p Platform) (findings []Finding, cleaned bool) {
	add := func(code string, ok bool, detail string) {
		status := "FAIL"
		if ok {
			status = "PASS"
		}
		findings = append(findings, Finding{code, status, detail})
	}
	base := filepath.Join(local, "isolation-probes")
	if err := workstore.CheckLinks(base); err != nil {
		add("FIXTURE_SETUP", false, err.Error())
		return
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		add("FIXTURE_SETUP", false, err.Error())
		return
	}
	id := workstore.ID()
	root := filepath.Join(base, "probe-"+id)
	err := os.Mkdir(root, 0700)
	if err != nil {
		add("FIXTURE_SETUP", false, err.Error())
		return
	}
	var removeProfile func() error
	link := ""
	pending := Pending{Version: 1, ID: id, Root: root, Profile: "AIDock.Probe." + id, Host: p.Host}
	defer func() {
		// Remove only our known junction before recursive fixture deletion.
		var e error
		if link != "" {
			path, _ := windows.UTF16PtrFromString(link)
			e = windows.RemoveDirectory(path)
		}
		if removeProfile != nil {
			if pe := removeProfile(); e == nil {
				e = pe
			}
		}
		if e == nil {
			e = removeFixture(pending)
		}
		cleaned = e == nil
		detail := "临时目录、测试 junction 与临时 AppContainer profile 已清理"
		if e != nil {
			detail = "清理未完成，保留恢复标记 " + root + "：" + e.Error()
		}
		add("FIXTURE_CLEANUP", cleaned, detail)
	}()
	if err = writePending(pending); err != nil {
		add("RECOVERY_MARKER", false, err.Error())
		return
	}
	if err = protect(root, "", ""); err != nil {
		add("FIXTURE_SETUP", false, err.Error())
		return
	}
	stage := filepath.Join(root, "stage")
	for _, dir := range []string{"stage/bin", "stage/input/package/assets", "stage/input/package/empty", "stage/output", "stage/scratch", "project", "private"} {
		if err = os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0700); err != nil {
			add("FIXTURE_SETUP", false, err.Error())
			return
		}
	}
	exe, err := executableBytes()
	if err != nil || workstore.Hash(exe) != p.Executable {
		add("EXECUTABLE_LOCK", false, "测试程序摘要变化；未启动")
		return
	}
	files := map[string][]byte{"stage/bin/probe.exe": exe, "stage/bin/locked.bin": []byte("locked-dependency"), "stage/input/package/SKILL.md": []byte(probeSkill), "stage/input/package/assets/data.bin": probeAsset, "project/sentinel.txt": []byte("project-private"), "private/sentinel.txt": []byte("local-private")}
	for name, data := range files {
		if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0600); err != nil {
			add("FIXTURE_SETUP", false, err.Error())
			return
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		add("FIXTURE_NETWORK", false, err.Error())
		return
	}
	defer listener.Close()
	// Prove the loopback endpoint is live outside the sandbox. A refused
	// connection to a dead endpoint must never count as enforced isolation.
	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		add("FIXTURE_NETWORK", false, err.Error())
		return
	}
	conn.Close()
	fixture := probeFixture{1, filepath.Join(root, "project", "sentinel.txt"), filepath.Join(root, "private", "sentinel.txt"), listener.Addr().String()}
	if err = os.WriteFile(filepath.Join(stage, "input", "fixture.json"), bytes(fixture), 0600); err != nil {
		add("FIXTURE_SETUP", false, err.Error())
		return
	}
	sid, cleanup, err := profile(pending.Profile)
	if err != nil {
		add("APPCONTAINER_CREATE", false, err.Error())
		return
	}
	removeProfile = cleanup
	if err = protect(root, sid.String(), "0x100020"); err != nil {
		add("FIXTURE_ACL", false, err.Error())
		return
	}
	if err = protect(stage, sid.String(), "0x1200a9"); err != nil {
		add("FIXTURE_ACL", false, err.Error())
		return
	}
	for _, dir := range []string{"bin", "input", "output", "scratch"} {
		access := "0x1200a9"
		if dir == "output" || dir == "scratch" {
			access = "0x1301bf"
		}
		err = protect(filepath.Join(stage, dir), sid.String(), access)
		if err != nil {
			add("FIXTURE_ACL", false, err.Error())
			return
		}
	}
	link = filepath.Join(stage, "input", "outside")
	if err = junction(link, filepath.Join(root, "private")); err != nil {
		add("FIXTURE_JUNCTION", false, err.Error())
		return
	}
	clean, err := launch(ctx, stage, sid, "check", 8*time.Second)
	add("JOB_EXIT_CLEANUP", clean, "正常退出后确认 Job 进程已经退出")
	if err != nil {
		add("PROBE_EXECUTION", false, err.Error())
		return
	}
	b, err := regularFile(filepath.Join(stage, "output", "result.json"))
	var child []Finding
	if err != nil || json.Unmarshal(b, &child) != nil || len(child) != 16 {
		add("PROBE_RESULT", false, fmt.Sprintf("测试结果不完整: %v, %d", err, len(child)))
		return
	}
	findings = append(findings, child...)
	stagedExe, exeErr := os.ReadFile(filepath.Join(stage, "bin", "probe.exe"))
	add("EXECUTABLE_LOCK", exeErr == nil && workstore.Hash(stagedExe) == p.Executable, "宿主确认实际执行的测试程序与批准摘要一致")
	unchanged := true
	for name, before := range files {
		if name == "stage/bin/probe.exe" {
			continue
		}
		after, e := regularFile(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil || string(after) != string(before) {
			unchanged = false
		}
	}
	add("HOST_SENTINELS_UNCHANGED", unchanged, "宿主核对输入、依赖及外部测试哨兵字节没有变化")
	outputOK := true
	for name, expected := range map[string][]byte{"SKILL.md": []byte(probeSkill), "assets/data.bin": probeAsset} {
		b, e := regularFile(filepath.Join(stage, "output", "package", filepath.FromSlash(name)))
		if e != nil || string(b) != string(expected) {
			outputOK = false
		}
	}
	i, e := os.Lstat(filepath.Join(stage, "output", "package", "empty"))
	if e != nil || !i.IsDir() {
		outputOK = false
	}
	add("HOST_OUTPUT_VERIFIED", outputOK, "宿主验证生成包的正文、二进制附件及空目录")
	_, e = os.Lstat(filepath.Join(stage, "output", "child-escaped"))
	add("HOST_NO_CHILD_ESCAPE", os.IsNotExist(e), "未发现禁止子进程产生的标记")
	// A second fixed process deliberately stays alive. Timeout must kill and
	// reap it before any fixture or profile can be removed.
	clean, err = launch(ctx, stage, sid, "hang", 700*time.Millisecond)
	started, e := regularFile(filepath.Join(stage, "output", "hang-started"))
	add("TIMEOUT_JOB_CLEANUP", clean && errors.Is(err, context.DeadlineExceeded) && e == nil && string(started) == "started", "已启动的超时测试进程由 Job 终止并确认退出")
	// Check all ordinary paths after probing, excluding our deliberate junction.
	err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == link {
			return filepath.SkipDir
		}
		if strings.Contains(path, "outside"+string(filepath.Separator)) {
			return fmt.Errorf("unexpected junction traversal")
		}
		return workstore.CheckLinks(path)
	})
	add("OUTPUT_PATHS_SAFE", err == nil, "生成结果未包含额外链接或 reparse point")
	return
}
