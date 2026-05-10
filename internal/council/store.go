package council

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/logger"
)

var log = logger.New("council")

// Store provides database access for council meeting and motion data.
// The Store methods are split across this file (struct + constructor),
// store_read.go (queries returning view structs), and store_write.go
// (transactional save/update operations).
type Store struct {
	db *pgxpool.Pool
}

// NewStore creates a new council store.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}
