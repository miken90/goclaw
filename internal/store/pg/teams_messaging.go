package pg

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ============================================================
// Messages
// ============================================================

func (s *PGTeamStore) SendMessage(ctx context.Context, msg *store.TeamMessageData) error {
	if msg.ID == uuid.Nil {
		msg.ID = store.GenNewID()
	}
	msg.CreatedAt = time.Now()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_messages (id, team_id, from_agent_id, to_agent_id, content, message_type, read, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		msg.ID, msg.TeamID, msg.FromAgentID, msg.ToAgentID,
		msg.Content, msg.MessageType, false, msg.CreatedAt,
	)
	return err
}

func (s *PGTeamStore) GetUnread(ctx context.Context, teamID, agentID uuid.UUID) ([]store.TeamMessageData, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.team_id, m.from_agent_id, m.to_agent_id, m.content, m.message_type, m.read, m.created_at,
		 COALESCE(fa.agent_key, '') AS from_agent_key,
		 COALESCE(ta.agent_key, '') AS to_agent_key
		 FROM team_messages m
		 LEFT JOIN agents fa ON fa.id = m.from_agent_id
		 LEFT JOIN agents ta ON ta.id = m.to_agent_id
		 WHERE m.team_id = $1 AND (m.to_agent_id = $2 OR m.to_agent_id IS NULL) AND m.read = false
		 ORDER BY m.created_at`, teamID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessageRowsJoined(rows)
}

func (s *PGTeamStore) MarkRead(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE team_messages SET read = true WHERE id = $1`, messageID)
	return err
}

func scanMessageRowsJoined(rows *sql.Rows) ([]store.TeamMessageData, error) {
	var messages []store.TeamMessageData
	for rows.Next() {
		var d store.TeamMessageData
		var toAgentID *uuid.UUID
		if err := rows.Scan(
			&d.ID, &d.TeamID, &d.FromAgentID, &toAgentID,
			&d.Content, &d.MessageType, &d.Read, &d.CreatedAt,
			&d.FromAgentKey, &d.ToAgentKey,
		); err != nil {
			return nil, err
		}
		d.ToAgentID = toAgentID
		messages = append(messages, d)
	}
	return messages, rows.Err()
}
