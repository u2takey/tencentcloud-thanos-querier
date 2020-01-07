package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	DefaultPeriodSeconds = 60
	DefaultDelaySeconds  = 300

	EnvSecretId  = "TENCENTCLOUD_SECRET_ID"
	EnvSecretKey = "TENCENTCLOUD_SECRET_KEY"
)

type TencentCredential struct {
	SecretKey string `yaml:"secret_key"`
	SecretId  string `yaml:"secret_id"`
}

type TencentMetric struct {
	MetricsName string `yaml:"metric_name"`
	Namespace   string `yaml:"namespace"`
	// Labels        []string               `yaml:"labels"`
	// Dimensions    map[string]interface{} `yaml:"dimensions"`
	// Filters       map[string]interface{} `yaml:"filters"`
}

type TencentConfig struct {
	Credential     TencentCredential         `yaml:"credential"`
	Metrics        map[string]*TencentMetric `yaml:"metrics"`
	ExternalLabels map[string]string         `yaml:"external_labels"`
	filename       string
}

func NewConfig() *TencentConfig {
	return &TencentConfig{}
}

func (me *TencentConfig) LoadFile(filename string) (errRet error) {
	me.filename = filename
	content, err := ioutil.ReadFile(me.filename)
	if err != nil {
		errRet = err
		return
	}
	if err = yaml.UnmarshalStrict(content, me); err != nil {
		errRet = err
		return
	}
	if errRet = me.check(); errRet != nil {
		return errRet
	}
	me.fillDefault()
	return nil
}

func (me *TencentConfig) check() (errRet error) {

	if me.Credential.SecretKey == "" {
		me.Credential.SecretKey = os.Getenv(EnvSecretKey)
		me.Credential.SecretId = os.Getenv(EnvSecretId)
	}

	if me.Credential.SecretKey == "" || me.Credential.SecretId == "" {
		return fmt.Errorf("error, missing credential information")
	}
	for _, metric := range me.Metrics {
		if metric.MetricsName == "" {
			return fmt.Errorf("missing tc_metric_name")
		}
		if len(strings.Split(metric.Namespace, `/`)) != 2 {
			return fmt.Errorf("tc_namespace should be 'xxxxxx/productName' format")
		}
		//if len(metric.Dimensions) != 0 && (len(metric.Filters) != 0 || len(metric.Labels) != 0) {
		//	return fmt.Errorf("[tc_myself_dimensions] conflict with [tc_labels,tc_filters]")
		//}
	}

	return nil
}

func (me *TencentConfig) fillDefault() {
	//for index, metric := range me.Metrics {
	//	if metric.PeriodSeconds == 0 {
	//		me.Metrics[index].PeriodSeconds = DefaultPeriodSeconds
	//	}
	//	if metric.DelaySeconds == 0 {
	//		me.Metrics[index].DelaySeconds = DefaultDelaySeconds
	//	}
	//
	//	if metric.RangeSeconds == 0 {
	//		metric.RangeSeconds = metric.PeriodSeconds
	//	}
	//}
}
