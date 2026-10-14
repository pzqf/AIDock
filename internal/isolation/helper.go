package isolation

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Helper is a fixed test entry point, not a command launcher. Direct calls
// outside an actual AppContainer token are refused before reading any files.
func Helper(args []string) (bool, int) {
	if len(args) == 0 || args[0] != "__aidock_isolation_probe" {
		return false, 0
	}
	if len(args) != 2 || !appContainerToken() {
		return true, 91
	}
	switch args[1] {
	case "hang":
		if os.WriteFile("../output/hang-started", []byte("started"), 0600) != nil {
			return true, 92
		}
		for {
			time.Sleep(time.Second)
		}
	case "child":
		_ = os.WriteFile("../output/child-escaped", []byte("unexpected"), 0600)
		return true, 93
	case "check":
	default:
		return true, 91
	}
	f, err := os.Open("../input/fixture.json")
	if err != nil {
		return true, 92
	}
	var fixture probeFixture
	d := json.NewDecoder(io.LimitReader(f, 16<<10))
	d.DisallowUnknownFields()
	err = d.Decode(&fixture)
	f.Close()
	if err != nil || fixture.Version != 1 {
		return true, 92
	}
	findings := []Finding{}
	if os.WriteFile("../output/check-started", []byte("started"), 0600) != nil {
		return true, 92
	}
	add := func(code string, ok bool, detail string) {
		status := "FAIL"
		if ok {
			status = "PASS"
		}
		findings = append(findings, Finding{code, status, detail})
	}
	add("APPCONTAINER_TOKEN", true, "测试进程确认自身 AppContainer 令牌")
	skill, e1 := os.ReadFile("../input/package/SKILL.md")
	asset, e2 := os.ReadFile("../input/package/assets/data.bin")
	add("DECLARED_INPUT_READ", e1 == nil && e2 == nil && string(skill) == probeSkill && string(asset) == string(probeAsset), "声明输入正文和二进制附件可以读取")
	e1 = os.MkdirAll("../output/package/assets", 0700)
	e2 = os.MkdirAll("../output/package/empty", 0700)
	e3 := os.WriteFile("../output/package/SKILL.md", skill, 0600)
	e4 := os.WriteFile("../output/package/assets/data.bin", asset, 0600)
	add("DECLARED_OUTPUT_WRITE", e1 == nil && e2 == nil && e3 == nil && e4 == nil, "声明输出可生成完整测试包；宿主另行核对字节与空目录")
	add("SCRATCH_WRITE", os.WriteFile("scratch.bin", []byte("temporary"), 0600) == nil, "只授予独立暂存区临时写入")
	add("READ_ONLY_INPUT", denied(os.WriteFile("../input/package/SKILL.md", []byte("unexpected"), 0600)), "OS 拒绝修改只读输入")
	executable, _ := os.Executable()
	// Opening for write is sufficient; do not rely on Windows' running-image
	// sharing restriction as evidence of the ACL. A separate bin file is used.
	add("READ_ONLY_BINARY", denied(os.WriteFile("../bin/locked.bin", []byte("unexpected"), 0600)), "OS 拒绝修改执行依赖目录")
	for _, path := range []struct{ code, path string }{{"PROJECT", fixture.Project}, {"LOCAL", fixture.Local}, {"JUNCTION", "../input/outside/sentinel.txt"}} {
		_, err = os.ReadFile(path.path)
		add(path.code+"_READ_DENIED", denied(err), "OS 拒绝读取未授予的专用测试哨兵")
		add(path.code+"_WRITE_DENIED", denied(os.WriteFile(path.path, []byte("unexpected"), 0600)), "OS 拒绝覆盖未授予的专用测试哨兵")
	}
	for _, endpoint := range []struct{ code, network, address string }{{"LOOPBACK_TCP_DENIED", "tcp4", fixture.Loopback}, {"OUTBOUND_TCP_DENIED", "tcp4", "192.0.2.1:443"}, {"OUTBOUND_UDP_DENIED", "udp4", "192.0.2.1:53"}} {
		conn, e := net.DialTimeout(endpoint.network, endpoint.address, 700*time.Millisecond)
		if e == nil && endpoint.network == "udp4" {
			_, e = conn.Write([]byte("aidock-isolation-probe"))
		}
		if conn != nil {
			conn.Close()
		}
		findings = append(findings, networkFinding(endpoint.code, endpoint.network, e))
	}
	err = exec.Command(executable, "__aidock_isolation_probe", "child").Run()
	add("CHILD_PROCESS_DENIED", childDenied(err), "OS 子进程创建策略拒绝启动；不以 Job 清理冒充拒绝创建")
	if os.WriteFile("../output/result.json", bytes(findings), 0600) != nil {
		return true, 92
	}
	return true, 0
}

func networkFinding(code, network string, err error) Finding {
	f := Finding{Code: code, Status: "UNKNOWN", Detail: "未取得系统拒绝证据；超时或 UDP 发送返回成功不能证明流量被阻断"}
	if denied(err) {
		f.Status, f.Detail = "PASS", "系统明确拒绝直接套接字访问"
	} else if err == nil && network != "udp4" {
		f.Status, f.Detail = "FAIL", "直接 TCP 连接成功，网络边界未阻断"
	}
	if err != nil {
		f.Detail += "：" + err.Error()
	}
	return f
}

const probeSkill = "---\nname: isolation-fixture\ndescription: AIDock fixed isolation fixture\n---\n# Isolated package\n"

var probeAsset = []byte{0, 255, 1, 128, 10, 13, 0}

type probeFixture struct {
	Version  int    `json:"schema_version"`
	Project  string `json:"project_sentinel"`
	Local    string `json:"local_sentinel"`
	Loopback string `json:"loopback"`
}

func regularFile(path string) ([]byte, error) {
	// Do not ingest helper-created reparse points as successful evidence.
	if err := checkProbePath(path); err != nil {
		return nil, err
	}
	i, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !i.Mode().IsRegular() || i.Size() > 64<<10 {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(filepath.Clean(path))
}
