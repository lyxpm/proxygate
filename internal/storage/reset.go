package storage

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ResetConfigVersionsFile clears historical configuration versions in a
// restored database. The next successful apply creates version 1.
func ResetConfigVersionsFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)+"?mode=rw"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if !db.Migrator().HasTable("config_versions") {
		return nil
	}
	if err := db.Exec("DELETE FROM config_versions").Error; err != nil {
		return err
	}
	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return err
	}
	return db.Exec("VACUUM").Error
}
