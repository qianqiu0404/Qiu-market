package bybit

import (
	"fmt"
	"testing"
)

func TestFetchOrderBook(t *testing.T) {
	client, err := NewBybitClient("http://127.0.0.1:7890", "http")
	if err != nil {
		panic(err)
	}

	book, err := client.FetchOrderBook("BTC/USDT")
	if err != nil {
		panic(err)
	}
	fmt.Println(book)
	fmt.Println("bids:", len(book.Bids))
	fmt.Println("asks:", len(book.Asks))
}
