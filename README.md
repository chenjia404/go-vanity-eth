# Go-Vanity-ETH

ETH靓号地址生成器，使用 github.com/ethereum/go-ethereum 的方法生成，更加安全可靠。

本程序运行后，会自动根据cpu数量，生成cpu数量-1的线程，批量的重复尝试生成符合要求的靓号地址，并写入 wallet.txt 文件里面。

## 为什么使用靓号地址

你可以设置你的地址8个8，或者8个6，更酷炫。

## config说明

continuous是连续的位数，比如8，意味着生成的地址尾部中必须有不少于8个连续的相同字符，dreamAddressSubstr是要求生成地址中有相同的字符串
注意地址长度，不要超出
## 使用

```
go run mian.go
```

## 编译

```
go build
```

window 运行 go-vanity-eth.exe 文件，其它系统运行 go-vanity-eth 文件。

## 安全提示

本程序使用了官方的 go-ethereum 的钱包私钥随机生成实现，具体实现是使用 go 的 crypto/rand 生成随机数，在 Linux 系统随机源是 /dev/urandom ，window 系统下使用 RtlGenRandom API。

理论上随机性强度足够高，但是本应用免费提供，不提供任何安全承诺。