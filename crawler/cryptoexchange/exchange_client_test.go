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

	book, err := client.FetchOrderBook("binance", "BTC/USDT")
	if err != nil {
		panic(err)
	}
	fmt.Println(book)
	fmt.Println("bids:", len(book.Bids))
	fmt.Println("asks:", len(book.Asks))
}
