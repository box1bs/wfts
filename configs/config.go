package configs

import (
	"encoding/json"
	"os"
)

type ConfigData struct {
	BaseURLs       			[]string 	`json:"base_urls" validate:"required,len=1:500"`
	LogPath   				string   	`json:"info_log_path"`
	BackupPath				string		`json:"backup_path" validate:"required"`
	IndexPath     			string   	`json:"index_path" validate:"required"`
	WorkersCount   			int      	`json:"worker_count" validate:"min=50,max=2000"`
	MaxTypo	  				int      	`json:"max_typo" validate:"min=1,max=4"`
	OnlySameDomain 			bool     	`json:"only_same_domain"`
}

func (cfg *ConfigData) Validate() error {
	return New().Validate(*cfg)
}

func UploadLocalConfiguration(fileName string) (*ConfigData, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}

	var cfg ConfigData
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}

	return &cfg, err
}