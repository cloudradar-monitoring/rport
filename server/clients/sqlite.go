package clients

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/cloudradar-monitoring/rport/db/migration/clients"
	"github.com/cloudradar-monitoring/rport/db/sqlite"
)

type ClientProvider interface {
	GetAll(ctx context.Context) ([]*Client, error)
	Save(ctx context.Context, client *Client) error
	DeleteObsolete(ctx context.Context) error
	Close() error
}

type SqliteProvider struct {
	db              *sqlx.DB
	keepLostClients time.Duration
}

func NewSqliteProvider(dbPath string, keepLostClients time.Duration) (*SqliteProvider, error) {
	db, err := sqlite.New(dbPath, clients.AssetNames(), clients.Asset)
	if err != nil {
		return nil, fmt.Errorf("failed to create clients DB instance: %v", err)
	}
	return &SqliteProvider{db: db, keepLostClients: keepLostClients}, nil
}

func (p *SqliteProvider) GetAll(ctx context.Context) ([]*Client, error) {
	var res []*clientSqlite
	err := p.db.SelectContext(
		ctx,
		&res,
		"SELECT * FROM clients WHERE disconnected_at IS NULL OR DATETIME(disconnected_at) >= DATETIME(?)",
		p.keepLostClientsStart(),
	)
	if err != nil {
		return nil, err
	}
	return convertClientList(res), nil
}

func (p *SqliteProvider) get(ctx context.Context, id string) (*Client, error) {
	res := &clientSqlite{}
	err := p.db.GetContext(ctx, res, "SELECT * FROM clients WHERE id = ?", id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return res.convert(), nil
}

func (p *SqliteProvider) Save(ctx context.Context, client *Client) error {
	_, err := p.db.NamedExecContext(
		ctx,
		"INSERT OR REPLACE INTO clients (id, client_auth_id, disconnected_at, details) VALUES (:id, :client_auth_id, :disconnected_at, :details)",
		convertToSqlite(client),
	)
	return err
}

func (p *SqliteProvider) DeleteObsolete(ctx context.Context) error {
	_, err := p.db.ExecContext(
		ctx,
		"DELETE FROM clients WHERE disconnected_at IS NOT NULL AND DATETIME(disconnected_at) < DATETIME(?)",
		p.keepLostClientsStart(),
	)
	return err
}

func (p *SqliteProvider) keepLostClientsStart() time.Time {
	return now().Add(-p.keepLostClients)
}

func convertToSqlite(v *Client) *clientSqlite {
	if v == nil {
		return nil
	}
	res := &clientSqlite{
		ID:           v.ID,
		ClientAuthID: v.ClientAuthID,
		Details: &clientDetails{
			Name:     v.Name,
			OS:       v.OS,
			OSArch:   v.OSArch,
			OSFamily: v.OSFamily,
			OSKernel: v.OSKernel,
			Hostname: v.Hostname,
			Version:  v.Version,
			Address:  v.Address,
			IPv4:     v.IPv4,
			IPv6:     v.IPv6,
			Tags:     v.Tags,
			Tunnels:  v.Tunnels,
		},
	}
	if v.DisconnectedAt != nil {
		res.DisconnectedAt = sql.NullTime{Time: *v.DisconnectedAt, Valid: true}
	}
	return res
}

type clientSqlite struct {
	ID             string         `db:"id"`
	ClientAuthID   string         `db:"client_auth_id"`
	DisconnectedAt sql.NullTime   `db:"disconnected_at"` // DisconnectedAt is a time when a client was disconnected. If nil - it's connected.
	Details        *clientDetails `db:"details"`
}

type clientDetails struct {
	Name     string    `json:"name"`
	OS       string    `json:"os"`
	OSArch   string    `json:"os_arch"`
	OSFamily string    `json:"os_family"`
	OSKernel string    `json:"os_kernel"`
	Hostname string    `json:"hostname"`
	Version  string    `json:"version"`
	Address  string    `json:"address"`
	IPv4     []string  `json:"ipv4"`
	IPv6     []string  `json:"ipv6"`
	Tags     []string  `json:"tags"`
	Tunnels  []*Tunnel `json:"tunnels"`
}

func (d *clientDetails) Scan(value interface{}) error {
	if d == nil {
		return errors.New("'details' cannot be nil")
	}
	valueStr, ok := value.(string)
	if !ok {
		return fmt.Errorf("expected to have string, got %T", value)
	}
	err := json.Unmarshal([]byte(valueStr), d)
	if err != nil {
		return fmt.Errorf("failed to decode 'details' field: %v", err)
	}
	return nil
}

func (d *clientDetails) Value() (driver.Value, error) {
	if d == nil {
		return nil, errors.New("'details' cannot be nil")
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("failed to encode 'details' field: %v", err)
	}
	return string(b), nil
}

func (s *clientSqlite) convert() *Client {
	d := s.Details
	res := &Client{
		ID:           s.ID,
		ClientAuthID: s.ClientAuthID,
		Name:         d.Name,
		OS:           d.OS,
		OSArch:       d.OSArch,
		OSFamily:     d.OSFamily,
		OSKernel:     d.OSKernel,
		Hostname:     d.Hostname,
		IPv4:         d.IPv4,
		IPv6:         d.IPv6,
		Tags:         d.Tags,
		Version:      d.Version,
		Address:      d.Address,
		Tunnels:      d.Tunnels,
	}
	if s.DisconnectedAt.Valid {
		res.DisconnectedAt = &s.DisconnectedAt.Time
	}
	return res
}

func (p *SqliteProvider) Close() error {
	return p.db.Close()
}

func convertClientList(list []*clientSqlite) []*Client {
	res := make([]*Client, 0, len(list))
	for _, cur := range list {
		res = append(res, cur.convert())
	}
	return res
}
