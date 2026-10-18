package auth

import (
	"errors"
	"testing"
)

type rowCount struct {
	rows int64
	err  error
}

func (c rowCount) RowsAffected() (int64, error) { return c.rows, c.err }

// A code is spent by a conditional write that must move exactly one row. Zero
// means another request spent it first, which is the guesser's failure. A
// driver that cannot count the rows is the database failing, and must not be
// reported as a wrong code.
func TestSpendingACodeNeedsExactlyOneRow(t *testing.T) {
	if err := spentOne(rowCount{rows: 1}); err != nil {
		t.Fatalf("one row moved: %v", err)
	}
	for _, rows := range []int64{0, 2} {
		if err := spentOne(rowCount{rows: rows}); !errors.Is(err, ErrTwoFactorCode) {
			t.Fatalf("%d rows moved: %v, want ErrTwoFactorCode", rows, err)
		}
	}
}

func TestADriverThatCannotCountRowsIsAFaultNotAWrongCode(t *testing.T) {
	driverErr := errors.New("driver cannot report rows affected")
	err := spentOne(rowCount{err: driverErr})
	if !errors.Is(err, driverErr) {
		t.Fatalf("the driver error was not returned: %v", err)
	}
	if errors.Is(err, ErrTwoFactorCode) {
		t.Fatal("a driver fault was reported as a wrong code")
	}
}
