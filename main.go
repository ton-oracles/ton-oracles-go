package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/robfig/cron/v3"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BinanceResponse struct {
	Mins      int    `json:"mins"`
	Price     string `json:"price"`
	CloseTime int64  `json:"closeTime"`
}

type OkxResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		InstId   string `json:"instId"`
		InstType string `json:"instType"`
		MarkPx   string `json:"markPx"`
		Ts       string `json:"ts"`
	} `json:"data"`
}

func main() {
	fmt.Println("start...")
	c := cron.New()
	_, err := c.AddFunc("0 0 * * *", updatePrice)
	if err != nil {
		fmt.Println("add task err:", err)
		return
	}
	c.Start()
	defer c.Stop()

	select {}
}

func updatePrice() {
	client := liteclient.NewConnectionPool()

	// add connect to testnet
	configUrl := "https://ton-blockchain.github.io/testnet-global.config.json"
	err := client.AddConnectionsFromConfigUrl(context.Background(), configUrl)
	if err != nil {
		panic(err)
	}

	// initialize ton api lite connection wrapper
	api := ton.NewAPIClient(client).WithRetry()

	// we need fresh block info to run get methods
	block, err := api.CurrentMasterchainInfo(context.Background())
	if err != nil {
		log.Fatalln("get block err:", err.Error())
		return
	}

	contractAddr := address.MustParseAddr("EQB_C3Jt6Dvgv5hp2B0Beg39BxMPzh9A0aqooq117iSQn3XE")
	res, err := api.RunGetMethod(context.Background(), block, contractAddr, "get_price_info")
	if err != nil {
		// if contract exit code != 0 it will be treated as an error too
		panic(err)
	}
	currentPrice := res.MustInt(0)
	dailyInitialPrice := res.MustInt(1)
	lastUpdatedAt := res.MustInt(2)
	lastUpdatedAtDate := time.Unix(lastUpdatedAt.Int64(), 0)
	priceAdminAddress := res.MustSlice(3).MustLoadAddr()
	superAdminAddress := res.MustSlice(4).MustLoadAddr()
	fmt.Printf("current price: %d\n", currentPrice)
	fmt.Println("last update at:", lastUpdatedAtDate.Format("2006-01-02 15:04:05"))
	fmt.Printf("daily InitialPrice price: %d\n", dailyInitialPrice)
	fmt.Printf("price admin address: %s\n", priceAdminAddress.String())
	fmt.Printf("super admin address: %s\n", superAdminAddress.String())

	// get ton price from binance
	binanceResp, err := http.Get("https://api.binance.com/api/v3/avgPrice?symbol=TONUSDT")
	if err != nil {
		fmt.Println("get ton price from binance error:", err)
		return
	}
	defer binanceResp.Body.Close()

	var binanceResult BinanceResponse
	if err := json.NewDecoder(binanceResp.Body).Decode(&binanceResult); err != nil {
		log.Fatalf("get ton price from biance error: %v", err)
		return
	}

	// get ton price from okx
	resp, err := http.Get("https://www.okx.com/api/v5/public/mark-price?instType=MARGIN&instId=TON-USDT")
	if err != nil {
		fmt.Println("get ton price from okx error:", err)
		return
	}
	defer resp.Body.Close()

	var result OkxResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("data format error: %v", err)
		return
	}

	data := result.Data[0]
	fmt.Println("get ton price from Binance and OKX")
	fmt.Println("Binance Ton Price:", binanceResult.Price)
	fmt.Println("OKX Ton Price:", data.MarkPx)

	// seed words of account, you can generate them with any wallet or using wallet.NewSeed() method
	words := strings.Split("loud earth predict amateur build verify beyond frequent fatigue nest lamp major memory ride belt area teach scene filter river board uniform possible control", " ")

	priceAdminWallet, err := wallet.FromSeed(api, words, wallet.V4R2)
	if err != nil {
		log.Fatalln("FromSeed err:", err.Error())
		return
	}
	balance, err := priceAdminWallet.GetBalance(context.Background(), block)
	fmt.Println(balance)
	if err != nil {
		log.Fatalln("GetBalance err:", err.Error())
		return
	}
	fmt.Println("current price admin address:", priceAdminWallet.WalletAddress().String())

	if balance.Nano().Uint64() >= 3000000 {
		// create transaction body cell, depends on what contract needs, just random example here
		binancePrice, _ := strconv.ParseFloat(binanceResult.Price, 32)
		newPrice, err := strconv.ParseFloat(data.MarkPx, 32)
		body := cell.BeginCell().
			MustStoreUInt(1002, 32).          // op code
			MustStoreUInt(rand.Uint64(), 64). // query id
			MustStoreUInt(uint64((newPrice+binancePrice)/2*100), 32).
			MustStoreUInt(uint64(time.Now().Unix()), 32).
			MustStoreUInt(0, 8).EndCell()

		log.Println("sending transaction and waiting for confirmation...")

		tx, block, err := priceAdminWallet.SendWaitTransaction(context.Background(), &wallet.Message{
			Mode: wallet.PayGasSeparately, // pay fees separately (from balance, not from amount)
			InternalMessage: &tlb.InternalMessage{
				Bounce:  true, // return amount in case of processing error
				DstAddr: contractAddr,
				Amount:  tlb.MustFromTON("0.01"),
				Body:    body,
			},
		})
		if err != nil {
			log.Fatalln("Send err:", err.Error())
			return
		}

		log.Println("transaction sent, confirmed at block, hash:", base64.StdEncoding.EncodeToString(tx.Hash))

		balance, err = priceAdminWallet.GetBalance(context.Background(), block)
		if err != nil {
			log.Fatalln("GetBalance err:", err.Error())
			return
		}

		log.Println("balance left:", balance.String())

	} else {
		log.Println("not enough balance:", balance.String())
	}
}
