package configs

import (
	"encoding/json"
	"os"
)

type ConfigData struct {
	BaseURLs       			[]string 	`json:"base_urls" validate:"required,len=1:500"`
	LogPath   				string   	`json:"info_log_path"`
	BackupPath				string		`json:"backup_path"`
	IndexPath     			string   	`json:"index_path" validate:"required"`
	WorkersCount   			int      	`json:"worker_count" validate:"min=50,max=2000"`
	MaxDepth       			int      	`json:"max_depth_crawl" validate:"min=1,max=10"`
	NGramCount    			int      	`json:"ngram_count" validate:"min=2,max=5"`
	MaxTypo	  				int      	`json:"max_typo" validate:"min=1,max=4"`
	ChunkSize 				int 		`json:"chunk_size" validate:"min=20,max=500"`
	OnlySameDomain 			bool     	`json:"only_same_domain"`
}

func (cfg *ConfigData) Validate() error {
	return New("validate").Validate(*cfg)
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