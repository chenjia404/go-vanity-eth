//go:build !windows || !amd64

package main

import "errors"

func newCUDASearcher(config, options) (searcher, error) {
	return nil, errors.New("当前 CUDA 后端支持 Windows amd64，请使用 CPU 后端或在 Windows 64 位环境运行")
}
