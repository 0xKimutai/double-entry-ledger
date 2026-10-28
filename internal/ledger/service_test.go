package ledger

import (
	"context"
	"errors"
	"testing"
)

type repositoryStub struct {
	createName     string
	createCurrency string
}

func (r *repositoryStub) CreateAccount(_ context.Context, name, currency string) (Account, error) {
	r.createName = name
	r.createCurrency = currency
	return Account{Name: name, Currency: currency}, nil
}
func (*repositoryStub) GetAccount(context.Context, string) (Account, error) {
	return Account{}, errors.New("unexpected GetAccount")
}
func (*repositoryStub) ListEntries(context.Context, string, Page) ([]Entry, error) {
	return nil, errors.New("unexpected ListEntries")
}
func (*repositoryStub) Deposit(context.Context, string, string, int64) (Transfer, bool, error) {
	return Transfer{}, false, errors.New("unexpected Deposit")
}
func (*repositoryStub) Transfer(context.Context, string, string, string, int64) (Transfer, bool, error) {
	return Transfer{}, false, errors.New("unexpected Transfer")
}
func TestCreateAccountNormalizesInput(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)

	_, err := service.CreateAccount(context.Background(), "  main wallet ", "kes")
	if err != nil {
		t.Fatal(err)
	}
	if repository.createName != "main wallet" {
		t.Errorf("name = %q, want %q", repository.createName, "main wallet")
	}
	if repository.createCurrency != "KES" {
		t.Errorf("currency = %q, want %q", repository.createCurrency, "KES")
	}
}

func TestServiceRejectsInvalidTransfer(t *testing.T) {
	service := NewService(&repositoryStub{})
	_, _, err := service.Transfer(context.Background(),
		"not-a-uuid", "also-not-a-uuid", "key", 100)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}

	_, _, err = service.Transfer(context.Background(),
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		"", 100)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
	}
}
