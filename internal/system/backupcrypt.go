package system

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/scrypt"
)

var backupMagic = []byte("PGBK1")
var backupAAD = []byte("ProxyGate backup v1")

const backupChunkSize = 64 * 1024

// EncryptBackupFile encrypts a plain backup ZIP into a password-protected
// stream. The password is the administrator confirmation password and is not
// stored in the file.
func EncryptBackupFile(srcPath, dstPath, password string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer dst.Close()

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce[:8]); err != nil {
		return err
	}
	header := append(append([]byte{}, backupMagic...), salt...)
	header = append(header, nonce...)
	if _, err := dst.Write(header); err != nil {
		return err
	}
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	buf := make([]byte, backupChunkSize)
	var counter uint32
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			chunkNonce := make([]byte, 12)
			copy(chunkNonce[:8], nonce[:8])
			binary.BigEndian.PutUint32(chunkNonce[8:], counter)
			enc := gcm.Seal(nil, chunkNonce, buf[:n], backupAAD)
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(len(enc)))
			if _, err := dst.Write(size[:]); err != nil {
				return err
			}
			if _, err := dst.Write(enc); err != nil {
				return err
			}
			counter++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	var zero [4]byte
	if _, err := dst.Write(zero[:]); err != nil {
		return err
	}
	return dst.Sync()
}

// DecryptBackup accepts either a legacy plain ZIP or a PGBK1 encrypted backup
// and returns a reader containing the ZIP data. The caller must close it.
func DecryptBackup(r io.Reader, password string) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(backupMagic))
	if err != nil {
		return nil, fmt.Errorf("read backup header: %w", err)
	}
	if string(head) != string(backupMagic) {
		// Legacy backups are plain ZIP files.
		return io.NopCloser(br), nil
	}
	if _, err := io.ReadFull(br, make([]byte, len(backupMagic))); err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(br, salt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(br, nonce); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		var counter uint32
		for {
			var size [4]byte
			if _, err := io.ReadFull(br, size[:]); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			n := binary.BigEndian.Uint32(size[:])
			if n == 0 {
				return
			}
			if n > backupChunkSize+16 {
				_ = pw.CloseWithError(errors.New("backup chunk is too large"))
				return
			}
			enc := make([]byte, n)
			if _, err := io.ReadFull(br, enc); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			chunkNonce := make([]byte, 12)
			copy(chunkNonce[:8], nonce[:8])
			binary.BigEndian.PutUint32(chunkNonce[8:], counter)
			plain, err := gcm.Open(nil, chunkNonce, enc, backupAAD)
			if err != nil {
				_ = pw.CloseWithError(errors.New("backup password is invalid or file is corrupted"))
				return
			}
			if _, err := pw.Write(plain); err != nil {
				return
			}
			counter++
		}
	}()
	return pr, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// IsEncryptedBackup reports whether the first bytes are a PGBK1 header.
func IsEncryptedBackup(data []byte) bool {
	return len(data) >= len(backupMagic) && string(data[:len(backupMagic)]) == string(backupMagic)
}
