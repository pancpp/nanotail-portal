package auth

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pancpp/nanotail-portal/database"
	"golang.org/x/crypto/bcrypt"
)

const (
	JWT_CONTEXT_KEY_USER_PID = "user_pid"
	JWT_CONTEXT_KEY_TOKEN    = "token"
	JWT_EXP_LEN              = 7 * 24 * time.Hour
)

var (
	gJwtSigningKey []byte = []byte("Rfz23tefYuhpB2iuTVhw")
)

type Claims struct {
	UserPID int64 `json:"pid"`
	jwt.RegisteredClaims
}

func Init(sign_key string) {
	gJwtSigningKey = []byte(sign_key)
}

func GetJwtSignKey() []byte {
	return gJwtSigningKey
}

// Create JWT token
func CreateJwtToken(userPID int64) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserPID: userPID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(JWT_EXP_LEN)),
		},
	}

	// Generate token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign the token
	return token.SignedString(gJwtSigningKey)
}

// AuthenticateWithUsernamePassword returns the user for matching credentials.
// Usernames are case-insensitive; passwords are case-sensitive.
// Invalid credentials return ErrUnauthorized; database errors are propagated.
func AuthenticateWithUsernamePassword(username, password string) (*database.User, error) {
	if username == "" || password == "" {
		return nil, ErrUnauthorized
	}

	db := database.DB()
	ctx := database.Context()

	user := new(database.User)
	// SQLite has no ILIKE operator. Equality also treats % and _ literally.
	if err := db.NewSelect().Model(user).
		Where("username = ? COLLATE NOCASE", username).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		log.Println("(AuthenticateWithUsernamePassword) db err: ", err)
		return nil, err
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Passwd), []byte(password)); err != nil {
		return nil, ErrUnauthorized
	}
	return user, nil
}

func AuthenticateWithUserPIDPassword(userPID int64, password string) error {
	db := database.DB()
	ctx := database.Context()

	user := &database.User{PID: userPID}
	if err := db.NewSelect().Model(user).WherePK().Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		log.Println("(AuthenticateWithUserPIDPassword) db err: ", err)
		return err
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Passwd), []byte(password)); err != nil {
		return ErrUnauthorized
	}
	return nil
}
