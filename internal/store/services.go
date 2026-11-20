package store

import (
	"database/sql"
)

// EnvironmentService is one declared service after the last start.
type EnvironmentService struct {
	Name  string `json:"name"`
	Port  int    `json:"port,omitempty"`
	State string `json:"state"`
}

// EnvironmentServiceSet is the last start's service list. ParseError is
// set when the file was invalid and no service started.
type EnvironmentServiceSet struct {
	Services   []EnvironmentService `json:"services"`
	ParseError string               `json:"services_error,omitempty"`
}

const servicesParseRow = ""

// ReplaceEnvironmentServices replaces every service row for the
// environment. Call it on each start.
func ReplaceEnvironmentServices(s *Store, envID int64, set EnvironmentServiceSet) error {
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM environment_services WHERE environment_id=?`, envID); err != nil {
			return err
		}
		if set.ParseError != "" {
			_, err := tx.Exec(`INSERT INTO environment_services (environment_id, name, port, state, error) VALUES (CAST(? AS BIGINT), ?, 0, '', ?)`,
				envID, servicesParseRow, set.ParseError)
			return err
		}
		for _, svc := range set.Services {
			if _, err := tx.Exec(`INSERT INTO environment_services (environment_id, name, port, state, error) VALUES (CAST(? AS BIGINT), ?, CAST(? AS BIGINT), ?, '')`,
				envID, svc.Name, svc.Port, svc.State); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListEnvironmentServices returns the last start's services for envID.
func ListEnvironmentServices(s *Store, envID int64) (EnvironmentServiceSet, error) {
	rows, err := s.DB.Query(`SELECT name, port, state, error FROM environment_services WHERE environment_id=? ORDER BY name`, envID)
	if err != nil {
		return EnvironmentServiceSet{}, err
	}
	defer func() { _ = rows.Close() }()
	out := EnvironmentServiceSet{Services: []EnvironmentService{}}
	for rows.Next() {
		var name, state, parseErr string
		var port int
		if err := rows.Scan(&name, &port, &state, &parseErr); err != nil {
			return EnvironmentServiceSet{}, err
		}
		if name == servicesParseRow {
			out.ParseError = parseErr
			continue
		}
		out.Services = append(out.Services, EnvironmentService{Name: name, Port: port, State: state})
	}
	return out, rows.Err()
}

// ListSessionServices returns the last start's services of the session's
// environment.
func ListSessionServices(s *Store, sessionID int64) (EnvironmentServiceSet, error) {
	sess, err := GetSession(s, sessionID)
	if err != nil {
		return EnvironmentServiceSet{}, err
	}
	return ListEnvironmentServices(s, sess.EnvironmentID)
}
