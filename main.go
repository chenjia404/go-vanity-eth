package main

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	filename = "./wallet.txt"
	mutex    sync.Mutex
	wg       sync.WaitGroup
)

type config struct {
	Continuous         int      `json:"continuous"`
	DreamAddressSubstr []string `json:"dreamAddressSubstr"`
}

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

	fmt.Println("本程序会自动尝试生成符合config.json要求的eth钱包地址，可能需要几天到几周的时间")
	fmt.Println("config.json参数说明：continuous是连续的位数，比如8，意味着生成的地址中必须有不少于8个连续的相同字符，dreamAddressSubstr是要求生成地址中有相同的字符串")
	fmt.Println("cpu内核数量:", CPUNum)
	fmt.Println("你可以在多个电脑上运行本程序加快速度")
	fmt.Println("开始生成……")

	f.Close()
	// 打开json文件
	jsonFile, err := os.Open("config.json")

	// 最好要处理以下错误
	if err != nil {
		fmt.Println(err)
	}

	byteValue, _ := ioutil.ReadAll(jsonFile)

	var con config
	json.Unmarshal([]byte(byteValue), &con)
	jsonFile.Close()
	threadNum := CPUNum - 1
	wg.Add(1)
	for i := 0; i < threadNum; i++ {
		go createWallet(con.Continuous, con.DreamAddressSubstr)
	}
	wg.Wait()
}

func createWallet(strLen int, strSubstr []string) {
	var f *os.File
	f, _ = os.OpenFile(filename, os.O_APPEND, 0666) //打开文件
	str_length := strLen
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
		isGood := false
		endstr := address[42-str_length : 42]
		if strings.Count(endstr, string(endstr[0])) >= str_length {
			isGood = true
		}
		for _, valueStr := range strSubstr {
			if strings.Contains(address, valueStr) {
				isGood = true
				break
			}
		}
		if isGood {
			mutex.Lock()
			fmt.Println(address)
			privateKeyBytes := crypto.FromECDSA(privateKey)
			fmt.Println(hexutil.Encode(privateKeyBytes)[2:])
			f.WriteString(address)
			f.WriteString("\n")
			f.WriteString(hexutil.Encode(privateKeyBytes)[2:])
			f.WriteString("\n")
			f.WriteString("\n")
			f.Sync()
			mutex.Unlock()
		}

	}
}

func checkFileIsExist(filename string) bool {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false
	}
	return true
}
