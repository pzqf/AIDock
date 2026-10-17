package workstore

import (
	"time"
)

type RecoveryChange struct {
	Path    string `json:"path"`
	Before  *Entry `json:"before,omitempty"`
	After   *Entry `json:"after,omitempty"`
	Current *Entry `json:"current,omitempty"`
}

type RecoveryIssue struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

type RecoveryTarget struct {
	Path     string           `json:"path"`
	Root     string           `json:"root"`
	File     bool             `json:"file,omitempty"`
	Status   string           `json:"status"`
	Observed bool             `json:"current_available"`
	Changes  []RecoveryChange `json:"changes"`
	Issues   []RecoveryIssue  `json:"issues"`
}

type RecoveryTransaction struct {
	ID      string           `json:"id"`
	Action  string           `json:"action"`
	State   string           `json:"state"`
	Status  string           `json:"status"`
	Targets []RecoveryTarget `json:"targets"`
	Issues  []RecoveryIssue  `json:"issues"`
}

type RecoveryInspection struct {
	Version      int                   `json:"schema_version"`
	Observed     string                `json:"observed"`
	Identity     string                `json:"identity"`
	Status       string                `json:"status"`
	Pending      bool                  `json:"pending"`
	Transactions []RecoveryTransaction `json:"transactions"`
	Issues       []RecoveryIssue       `json:"issues"`
	Boundary     string                `json:"boundary"`
}

// InspectRecovery holds the same locks as Recover. It checks all targets and
// backup bytes without storing current file content, replacing files, deleting
// interrupted temporaries, clearing markers or changing transaction journals.
// The report is an observation, never permission to skip Recover's checks.
func (c *Coordinator) InspectRecovery() (RecoveryInspection, error) {
	r := RecoveryInspection{Version: 1, Observed: time.Now().UTC().Format(time.RFC3339Nano), Identity: c.Identity, Status: "IDLE", Transactions: []RecoveryTransaction{}, Issues: []RecoveryIssue{}, Boundary: "仅检查恢复条件，不替换项目文件、不保存当前文件正文、不清理临时文件或恢复标记。报告只代表本次观察；执行恢复仍会锁内重新检查。冲突时先另行保存人工修改，再将冲突路径恢复到事务前或事务后已知字节并重新检查；不要删除恢复标记或整批备份。"}
	err := c.locked(func() error {
		js, err := c.journals()
		if err != nil {
			return err
		}
		marked := map[string]bool{}
		for _, root := range c.Roots {
			v, err := c.barrier(root)
			if err != nil {
				r.Pending = true
				r.Issues = append(r.Issues, RecoveryIssue{"MARKER_INVALID", root.Root, err.Error()})
				continue
			}
			if v == nil {
				continue
			}
			r.Pending = true
			found := false
			for _, j := range js {
				if j.Plan.ID == v.ID && j.Plan.Digest == v.Digest {
					found, marked[j.Plan.ID] = true, true
					break
				}
			}
			if !found {
				r.Issues = append(r.Issues, RecoveryIssue{"BACKUP_UNAVAILABLE", root.Root, "恢复标记缺少匹配的完整事务备份；保留标记并找回原备份"})
			}
		}
		for _, j := range js {
			if j.State != "prepared" && !marked[j.Plan.ID] {
				continue
			}
			r.Pending = true
			tx := RecoveryTransaction{ID: j.Plan.ID, Action: j.Plan.Action, State: j.State, Status: "CLEANUP_ONLY", Targets: []RecoveryTarget{}, Issues: []RecoveryIssue{}}
			if j.State == "prepared" {
				tx.Status = "READY"
				if c.RecoveryValidate != nil {
					if err := c.RecoveryValidate(j.Plan); err != nil {
						tx.Status = "CONFLICT"
						tx.Issues = append(tx.Issues, RecoveryIssue{"IDENTITY_CONFLICT", "", err.Error()})
					}
				}
				if err := c.assets(j.Plan); err != nil {
					tx.Status = "CONFLICT"
					tx.Issues = append(tx.Issues, RecoveryIssue{"BACKUP_UNAVAILABLE", "", err.Error()})
				} else {
					for _, target := range j.Plan.Targets {
						s := c.store(target.Path)
						s.active = j.Plan.ID
						p := Plan{ID: j.Plan.ID, Before: target.Before, After: target.After, Changes: Changes(target.Before, target.After)}
						inspect := s.recoveryTreesWith
						if target.File {
							inspect = s.fileRecoveryTreesWith
						}
						now, _, err := inspect(p, true)
						row := RecoveryTarget{Path: target.Path, Root: s.Root, File: target.File, Status: "READY", Observed: now.Entries != nil, Changes: []RecoveryChange{}, Issues: []RecoveryIssue{}}
						if err == nil && target.Before.Exists && target.After.Exists && !now.Exists {
							row.Issues = append(row.Issues, RecoveryIssue{"ROOT_UNAVAILABLE", s.Root, "原目录不可用；恢复原目录后重试"})
						}
						if err != nil {
							row.Issues = append(row.Issues, RecoveryIssue{"RECOVERY_CONFLICT", s.Root, err.Error()})
						}
						for _, change := range p.Changes {
							item := RecoveryChange{Path: change.Path, Before: change.Before, After: change.After}
							if entry, ok := now.Entries[change.Path]; ok {
								item.Current = &entry
							}
							row.Changes = append(row.Changes, item)
						}
						if len(row.Issues) > 0 {
							row.Status, tx.Status = "CONFLICT", "CONFLICT"
						}
						tx.Targets = append(tx.Targets, row)
					}
				}
			}
			r.Transactions = append(r.Transactions, tx)
		}
		if r.Pending {
			r.Status = "READY"
			cleanup := len(r.Transactions) > 0
			for _, tx := range r.Transactions {
				cleanup = cleanup && tx.Status == "CLEANUP_ONLY"
				if tx.Status == "CONFLICT" {
					r.Status = "CONFLICT"
				}
			}
			if cleanup {
				r.Status = "CLEANUP_ONLY"
			}
			if len(r.Issues) > 0 {
				r.Status = "CONFLICT"
			}
		}
		return nil
	})
	return r, err
}
