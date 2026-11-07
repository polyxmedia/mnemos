package storage

import (
	"context"
	"fmt"
	"time"
)

// AssignedConversation is one host conversation with a retrieval arm. Several
// sessions can share a host ID when a conversation is resumed. They are one
// unit here, since they share one transcript and one assignment.
type AssignedConversation struct {
	HostID  string
	Arm     string
	Project string
}

// AssignedConversations returns conversations assigned an arm since the cutoff,
// oldest first. Aggregation crosses the session boundary, so it lives on DB
// directly like Digest and Efficacy.
func (d *DB) AssignedConversations(ctx context.Context, since time.Time) ([]AssignedConversation, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT host_session_id, MIN(retrieval_arm), COALESCE(MIN(project), '')
		  FROM sessions
		 WHERE retrieval_arm IS NOT NULL AND retrieval_arm != ''
		   AND host_session_id IS NOT NULL
		   AND started_at >= ?
		 GROUP BY host_session_id
		 ORDER BY MIN(started_at)`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("assigned conversations: %w", err)
	}
	defer rows.Close()

	var out []AssignedConversation
	for rows.Next() {
		var c AssignedConversation
		if err := rows.Scan(&c.HostID, &c.Arm, &c.Project); err != nil {
			return nil, fmt.Errorf("scan assigned conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
