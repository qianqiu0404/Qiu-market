//go:build integration

package cryptoexchange

import (
	"fmt"
	"testing"
)

func TestFetchOrderBook(t *testing.T) {
	client, err := NewExchangeClient("http://127.0.0.1:7890", "http")
	if err != nil {
		t.Fatal("err====", err)
	}

	book, err := client.FetchOrderBook("Bybit", "BTC/USDT")
	if err != nil {
		panic(err)
	}
	fmt.Println(book.Bids[0][0])
	fmt.Println(book.Asks[0][0])
	fmt.Println("bids:", len(book.Bids))
	fmt.Println("asks:", len(book.Asks))
}
