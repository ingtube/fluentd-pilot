package main

import (
	"flag"
	"io/ioutil"
	"os"

	"github.com/ingtube/fluentd_pilot/pilot"
	log "github.com/sirupsen/logrus"
)

func main() {
	template := flag.String("template", "", "Fluentd 配置文件模板")
	level := flag.String("log-level", "INFO", "日志级别")
	flag.Parse()

	if *template == "" {
		log.Fatalf("Fluentd 配置文件模板未设置")
	}

	log.SetOutput(os.Stdout)
	logLevel, err := log.ParseLevel(*level)
	if err != nil {
		log.Fatalf("解析日志级别失败：%s", err.Error())
	}
	log.SetLevel(logLevel)
	log.SetFormatter(&log.JSONFormatter{})

	b, err := ioutil.ReadFile(*template)
	if err != nil {
		log.Fatalf("读取 fluentd 配置模板失败：%s", err)
	}

	log.Fatal(pilot.Run(string(b)))
}
