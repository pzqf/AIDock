package cli

import (
	"github.com/pzqf/AIDock/internal/engine"
	"io"
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) credentialCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "secret", Short: "OS 凭据管理；无明文读取或导出"}
	group.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.credentialEngine()
		if err != nil {
			return err
		}
		out, err := e.CredentialState(cmd.Context())
		if err != nil {
			return err
		}
		return r.finish(result.New("secret list", out))
	}})
	for _, action := range []string{"add", "rotate", "delete"} {
		var revision, review string
		var stdin, confirm, reviewed bool
		c := &cobra.Command{Use: action + " <name>", Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || !credentials.ValidName(args[0]) {
				return result.Fail("INVALID_INPUT", "需要一个有效密钥名称；值只能通过 stdin 输入", 2)
			}
			return nil
		}, Short: "值只从 stdin 读取，不接受命令行值参数", RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return result.Fail("CONFIRMATION_REQUIRED", "需要 --confirm；密钥轮换/删除不能通过普通回退撤销", 2)
			}
			if action != "delete" && !stdin {
				return result.Fail("INVALID_INPUT", "新增和轮换需要 --stdin；推荐从未回显的输入或安全管道传入", 2)
			}
			e, err := r.credentialEngine()
			if err != nil {
				return err
			}
			req := credentials.Edit{Action: action, Name: args[0], ExpectedRevision: revision, Confirm: confirm, ReferencesReviewed: reviewed, ExpectedReferences: review}
			if action != "delete" {
				b, readErr := io.ReadAll(io.LimitReader(cmd.InOrStdin(), credentials.MaxValue+1))
				if readErr != nil || len(b) > credentials.MaxValue {
					clear(b)
					return result.Fail("INVALID_INPUT", "stdin 不可读或超过 2400 字节", 2)
				}
				req.Value = string(b)
				clear(b)
			}
			out, err := e.ChangeCredential(cmd.Context(), req)
			req.Value = ""
			if err != nil {
				return err
			}
			return r.finish(result.New("secret "+action, out))
		}}
		c.Flags().StringVar(&revision, "expected-revision", "", "list 返回的当前修订")
		c.Flags().BoolVar(&confirm, "confirm", false, "确认系统凭据操作；旧值不进入恢复包")
		if action != "delete" {
			c.Flags().BoolVar(&stdin, "stdin", false, "精确读取 stdin 字节；不会自动裁剪换行")
		}
		if action == "delete" {
			c.Flags().BoolVar(&reviewed, "references-reviewed", false, "确认已阅读已知引用及未知范围")
			c.Flags().StringVar(&review, "expected-references", "", "list 返回的 review 摘要")
		}
		group.AddCommand(c)
	}
	var confirm bool
	reconcile := &cobra.Command{Use: "reconcile", Args: cobra.NoArgs, Short: "只核对中断操作的系统修订，不恢复旧值", RunE: func(cmd *cobra.Command, _ []string) error {
		if !confirm {
			return result.Fail("CONFIRMATION_REQUIRED", "需要 --confirm 核对中断操作", 2)
		}
		e, err := r.credentialEngine()
		if err != nil {
			return err
		}
		m, err := e.Credentials()
		if err == nil {
			err = m.Reconcile(cmd.Context())
		}
		if err != nil {
			return err
		}
		return r.finish(result.New("secret reconcile", map[string]bool{"reconciled": true}))
	}}
	reconcile.Flags().BoolVar(&confirm, "confirm", false, "确认核对 OS 凭据修订并追加结果审计")
	group.AddCommand(reconcile)
	group.SetIn(os.Stdin)
	root.AddCommand(group)
}

func (r *runner) credentialEngine() (*engine.Engine, error) {
	e, err := r.load(false)
	if err == nil && r.o.home != "" {
		e.Roots.Local = filepath.Join(e.Roots.Home, "local")
	}
	return e, err
}
