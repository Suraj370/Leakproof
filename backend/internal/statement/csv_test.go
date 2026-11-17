package statement

import (
	"errors"
	"strings"
	"testing"

	"leakproof/internal/cards"
)

const hdfcStyle = `Account Number,XXXX1234
Statement From,01/01/2025 To 31/03/2025

Date,Narration,Chq./Ref.No.,Value Dt,Withdrawal Amt.,Deposit Amt.,Closing Balance
05/01/25,UPI-SWIGGY-9988,0001,05/01/25,"1,200.00",,50000
09/01/25,CRED RENT PAYMENT,0002,09/01/25,"25,000.00",,25000
15/02/25,POS 4912 IOCL PETROL PUMP,0003,15/02/25,"3,000.00",,22000
20/02/25,SALARY CREDIT,0004,20/02/25,,"80,000.00",102000
10/03/25,AMAZON.IN MARKETPLACE,0005,10/03/25,"6,000.00",,96000
11/03/25,UPI/RAMESH TEA STALL,0006,11/03/25,"600.00",,95400
Total,,,,,,
`

func TestParseDebitColumnFormat(t *testing.T) {
	s, err := ParseCSV(strings.NewReader(hdfcStyle))
	if err != nil {
		t.Fatal(err)
	}
	if s.Months != 3 || s.Transactions != 5 {
		t.Fatalf("%+v", s)
	}
	// Totals over 3 months: dining 1,200; excluded 25,000; fuel 3,000; online 6,000; offline 600.
	want := map[cards.Category]int64{
		cards.Dining: 40_000, cards.Excluded: 833_333, cards.Fuel: 100_000, cards.OnlineShopping: 200_000, cards.OfflinePOS: 20_000,
	}
	for cat, w := range want {
		if s.MonthlySpendPaise[cat] != w {
			t.Errorf("%s = %d, want %d", cat, s.MonthlySpendPaise[cat], w)
		}
	}
	if len(s.LowConfidence) != 1 || s.LowConfidence[0].Description != "UPI/RAMESH TEA STALL" {
		t.Fatalf("%+v", s.LowConfidence)
	}
}

func TestSalaryCreditIsNotSpend(t *testing.T) {
	s, _ := ParseCSV(strings.NewReader(hdfcStyle))
	var sum int64
	for _, v := range s.MonthlySpendPaise {
		sum += v
	}
	if sum > 1_300_000 { // far below 3 months of Rs80k credit
		t.Fatalf("credit counted as spend: %d", sum)
	}
}

func TestParseSignedAmountWithDrCr(t *testing.T) {
	csv := "Transaction Date,Description,Amount,Dr/Cr\n" +
		"2025-01-03,ZOMATO ORDER,500.00,Dr\n" +
		"2025-01-04,REFUND ZOMATO,200.00,Cr\n" +
		"2025-01-05,INDIGO AIRLINES,\"4,500.50\",Dr\n"
	s, err := ParseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if s.Months != 1 || s.MonthlySpendPaise[cards.Dining] != 50_000 || s.MonthlySpendPaise[cards.FlightsHotels] != 450_050 {
		t.Fatalf("%+v", s)
	}
}

func TestParseNegativeAmounts(t *testing.T) {
	csv := "Date,Particulars,Amount\n03 Jan 2025,BLINKIT,-800.00\n04 Jan 2025,SALARY,50000\n"
	s, err := ParseCSV(strings.NewReader(csv))
	if err != nil || s.MonthlySpendPaise[cards.Groceries] != 80_000 || s.Transactions != 1 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestMonthsAveragingAcrossGap(t *testing.T) {
	csv := "Date,Description,Debit\n01/01/2025,BLINKIT,\"1,000\"\n01/03/2025,BLINKIT,\"1,000\"\n"
	s, _ := ParseCSV(strings.NewReader(csv))
	if s.Months != 2 || s.MonthlySpendPaise[cards.Groceries] != 100_000 { // Rs2,000 over 2 observed months
		t.Fatalf("%+v", s)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := ParseCSV(strings.NewReader("")); !errors.Is(err, ErrNoHeader) {
		t.Fatalf("%v", err)
	}
	if _, err := ParseCSV(strings.NewReader("foo,bar\n1,2\n")); !errors.Is(err, ErrNoHeader) {
		t.Fatalf("%v", err)
	}
	if _, err := ParseCSV(strings.NewReader("Date,Description,Debit\n")); err == nil {
		t.Fatal("expected no-transactions error")
	}
}

func TestMalformedLinesDoNotAbort(t *testing.T) {
	// stray quote mid-field, short row and a footer line: all tolerated
	csv := "Date,Description,Debit\n01/01/2025,BLI\"NKIT,500\n02/01/2025\nEnd of statement\n01/01/2025,ZOMATO,300\n"
	s, err := ParseCSV(strings.NewReader(csv))
	if err != nil || s.Transactions != 2 {
		t.Fatalf("%v %+v", err, s)
	}
}
