package main

import (
	"crypto/ecdsa"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

var filename = "./wallet.txt"

func main() {
	var f *os.File
	CPUNum := runtime.NumCPU()
	if checkFileIsExist(filename) { //如果文件存在
		f, _ = os.OpenFile(filename, os.O_APPEND, 0666) //打开文件
		fmt.Println("文件存在")
	} else {
		f, _ = os.Create(filename) //创建文件
		fmt.Println("文件不存在")
	}

	fmt.Println("本程序会自动尝试生成尾号是8个重复字符的eth钱包地址，可能需要几天到几周的时间")
	fmt.Println("cpu内核数量:", CPUNum)
	fmt.Println("你可以在多个电脑上运行本程序加快速度")
	fmt.Println("开始生成……")

	f.Close()
	threadNum := CPUNum - 1
	for i := 0; i < threadNum; i++ {
		go createWallet()
	}
	createWallet()
}

func createWallet() {
	var f *os.File
	f, _ = os.OpenFile(filename, os.O_APPEND, 0666) //打开文件
	str_length := 8
	for {
		privateKey, err := crypto.GenerateKey()
		if err != nil {
			log.Fatal(err)
		}

		publicKey := privateKey.Public()
		publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			log.Fatal("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
		}

		address := crypto.PubkeyToAddress(*publicKeyECDSA).Hex()
		isGood := true
		endstr := address[42-str_length : 42]
		if strings.Count(endstr, string(endstr[0])) != str_length {
			isGood = false
		}
		if isGood {
			fmt.Println(address)
			privateKeyBytes := crypto.FromECDSA(privateKey)
			fmt.Println(hexutil.Encode(privateKeyBytes)[2:])
			f.WriteString(address)
			f.WriteString("\n")
			f.WriteString(hexutil.Encode(privateKeyBytes)[2:])
			f.WriteString("\n")
			f.WriteString("\n")
			f.Sync()
		}

	}
}

func checkFileIsExist(filename string) bool {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false
	}
	return true
}
