package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

type Operation struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Log       string `json:"log"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (a *App) StartOperation(kind string, run func(context.Context) (string, error)) (Operation, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	op := Operation{ID: hex.EncodeToString(raw), Kind: kind, Status: "running", CreatedAt: now, UpdatedAt: now}
	if _, err := a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES(?,?,?,?,?)", op.ID, op.Kind, op.Status, now, now); err != nil {
		return Operation{}, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		log, err := run(ctx)
		status, message := "success", ""
		if err != nil {
			status = "failed"
			message = err.Error()
		}
		_, _ = a.DB.Exec("UPDATE operations SET status=?,log=?,error=?,updated_at=? WHERE id=?", status, log, message, time.Now().UTC().Format(time.RFC3339), op.ID)
	}()
	return op, nil
}
func (a *App) operation(w http.ResponseWriter, r *http.Request) {
	var op Operation
	err := a.DB.QueryRowContext(r.Context(), "SELECT id,kind,status,log,error,created_at,updated_at FROM operations WHERE id=?", r.PathValue("id")).Scan(&op.ID, &op.Kind, &op.Status, &op.Log, &op.Error, &op.CreatedAt, &op.UpdatedAt)
	if err != nil {
		WriteError(w, 404, "not_found", "操作记录不存在")
		return
	}
	WriteJSON(w, 200, op)
}
