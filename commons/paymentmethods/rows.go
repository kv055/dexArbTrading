package paymentmethods

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
)

// MethodRow mirrors one payment_methods table row. Category holds the display
// name exactly as stored in the DB (e.g. "Fiat Transmitter"); use
// CategoryIDFromDisplay to get the API category ID.
type MethodRow struct {
	ID         int64
	Name       string
	MergeRegex string // "" when NULL: exact-name matching only
	AliasList  []string
	Category   string
	AccountID  *int64
	IsActive   bool
	IsLinked   bool
	Priority   int
	MatchOrder int
	Popularity int
	Sources    []string // DEX offer slugs (bisq, robosats, …)
	Currencies []string // ISO fiat codes (EUR, USD, …)
}

// TextArray scans and writes Postgres TEXT[] values without tying this shared
// package to a specific database driver.
type TextArray []string

func (a *TextArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case []string:
		*a = append((*a)[:0], v...)
		return nil
	case string:
		return a.scanLiteral(v)
	case []byte:
		return a.scanLiteral(string(v))
	default:
		return fmt.Errorf("scan text array: unsupported source %T", src)
	}
}

func (a TextArray) Value() (driver.Value, error) {
	return formatTextArrayLiteral([]string(a)), nil
}

func (a *TextArray) scanLiteral(s string) error {
	parsed, err := parseTextArrayLiteral(s)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

// LoadRows fetches every payment_methods row ordered for canonicalization
// (match_order, then id). Uses only database/sql so callers on GORM reach it
// via gormDB.DB().
func LoadRows(ctx context.Context, db *sql.DB) ([]MethodRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, COALESCE(merge_regex, ''), alias_list, category,
		       account_id,
		       is_active, is_linked, priority, match_order, popularity,
		       sources, currencies
		FROM payment_methods
		ORDER BY match_order, id`)
	if err != nil {
		return nil, fmt.Errorf("load payment_methods: %w", err)
	}
	defer rows.Close()

	var out []MethodRow
	for rows.Next() {
		var r MethodRow
		var aliases, sources, currencies TextArray
		if err := rows.Scan(&r.ID, &r.Name, &r.MergeRegex, &aliases, &r.Category,
			&r.AccountID,
			&r.IsActive, &r.IsLinked, &r.Priority, &r.MatchOrder, &r.Popularity,
			&sources, &currencies); err != nil {
			return nil, fmt.Errorf("scan payment_methods row: %w", err)
		}
		r.AliasList = []string(aliases)
		r.Sources = []string(sources)
		r.Currencies = []string(currencies)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment_methods: %w", err)
	}
	return out, nil
}

func parseTextArrayLiteral(s string) ([]string, error) {
	if s == "" || s == "{}" {
		return []string{}, nil
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("scan text array: invalid literal %q", s)
	}
	body := s[1 : len(s)-1]
	var out []string
	var b strings.Builder
	inQuotes := false
	escaped := false
	quoted := false
	flush := func() {
		val := b.String()
		if quoted || val != "NULL" {
			out = append(out, val)
		}
		b.Reset()
		quoted = false
	}
	for i := 0; i < len(body); i++ {
		ch := body[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if inQuotes {
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inQuotes = false
			default:
				b.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case '"':
			inQuotes = true
			quoted = true
		case ',':
			flush()
		default:
			b.WriteByte(ch)
		}
	}
	if escaped || inQuotes {
		return nil, fmt.Errorf("scan text array: invalid quoted literal %q", s)
	}
	flush()
	return out, nil
}

func formatTextArrayLiteral(values []string) string {
	if len(values) == 0 {
		return "{}"
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		quoted = append(quoted, `"`+v+`"`)
	}
	return "{" + strings.Join(quoted, ",") + "}"
}
