package jwt

import (
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/appleboy/gofight"
	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

// Login form structure.
type Login struct {
	Username string `form:"username" json:"username" binding:"required"`
	Password string `form:"password" json:"password" binding:"required"`
}

var (
	key                  = []byte("secret key")
	defaultAuthenticator = func(c *gin.Context) (interface{}, error) {
		var loginVals Login
		userID := loginVals.Username
		password := loginVals.Password

		if userID == "admin" && password == "admin" {
			return userID, nil
		}

		return userID, ErrFailedAuthentication
	}
)

func makeTokenString(SigningAlgorithm string, username string, scope string) string {
	if SigningAlgorithm == "" {
		SigningAlgorithm = "HS256"
	}

	if scope == "" {
		scope = "access"
	}

	token := jwt.New(jwt.GetSigningMethod(SigningAlgorithm))
	claims := token.Claims.(jwt.MapClaims)
	claims["identity"] = username
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["orig_iat"] = time.Now().Unix()
	claims["scope"] = scope
	var tokenString string
	if SigningAlgorithm == "RS256" {
		keyData, _ := ioutil.ReadFile("testdata/jwtRS256.key")
		signKey, _ := jwt.ParseRSAPrivateKeyFromPEM(keyData)
		tokenString, _ = token.SignedString(signKey)
	} else {
		tokenString, _ = token.SignedString(key)
	}

	return tokenString
}

func TestMissingKey(t *testing.T) {

	_, err := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,
	})

	assert.Error(t, err)
	assert.Equal(t, ErrMissingSecretKey, err)
}

func TestMissingPrivKey(t *testing.T) {
	_, err := New(&GinJWTMiddleware{
		Realm:            "zone",
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "nonexisting",
	})

	assert.Error(t, err)
	assert.Equal(t, ErrNoPrivKeyFile, err)
}

func TestMissingPubKey(t *testing.T) {
	_, err := New(&GinJWTMiddleware{
		Realm:            "zone",
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "testdata/jwtRS256.key",
		PubKeyFile:       "nonexisting",
	})

	assert.Error(t, err)
	assert.Equal(t, ErrNoPubKeyFile, err)
}

func TestInvalidPrivKey(t *testing.T) {
	_, err := New(&GinJWTMiddleware{
		Realm:            "zone",
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "testdata/invalidprivkey.key",
		PubKeyFile:       "testdata/jwtRS256.key.pub",
	})

	assert.Error(t, err)
	assert.Equal(t, ErrInvalidPrivKey, err)
}

func TestInvalidPubKey(t *testing.T) {
	_, err := New(&GinJWTMiddleware{
		Realm:            "zone",
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "testdata/jwtRS256.key",
		PubKeyFile:       "testdata/invalidpubkey.key",
	})

	assert.Error(t, err)
	assert.Equal(t, ErrInvalidPubKey, err)
}

func TestMissingTimeOut(t *testing.T) {
	authMiddleware, err := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Authenticator: defaultAuthenticator,
	})

	assert.NoError(t, err)
	assert.Equal(t, time.Hour, authMiddleware.Timeout)
}

func TestMissingTokenLookup(t *testing.T) {
	authMiddleware, err := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Authenticator: defaultAuthenticator,
	})

	assert.NoError(t, err)
	assert.Equal(t, "header:Authorization", authMiddleware.TokenLookup)
}

func helloHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"text":  "Hello World.",
		"token": GetToken(c),
	})
}

func ginHandler(auth *GinJWTMiddleware) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.POST("/login", auth.LoginHandler)
	// test token in path
	r.GET("/g/:token/refresh_token", auth.RefreshHandler)

	group := r.Group("/auth")
	// Refresh time can be longer than token timeout
	group.POST("/refresh_token", auth.RefreshHandler)
	group.Use(auth.MiddlewareFunc())
	{
		group.GET("/hello", helloHandler)
	}

	return r
}

func TestMissingAuthenticatorForLoginHandler(t *testing.T) {

	authMiddleware, err := New(&GinJWTMiddleware{
		Realm:      "test zone",
		Key:        key,
		Timeout:    time.Hour,
		MaxRefresh: time.Hour * 24,
	})

	assert.NoError(t, err)

	handler := ginHandler(authMiddleware)
	r := gofight.New()

	r.POST("/login").
		SetJSON(gofight.D{
			"username": "admin",
			"password": "admin",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			message := gjson.Get(r.Body.String(), "message")

			assert.Equal(t, ErrMissingAuthenticatorFunc.Error(), message.String())
			assert.Equal(t, http.StatusInternalServerError, r.Code)
		})
}

func TestLoginHandler(t *testing.T) {

	// the middleware to test
	authMiddleware, err := New(&GinJWTMiddleware{
		Realm: "test zone",
		Key:   key,
		PayloadFunc: func(data interface{}) MapClaims {
			// Set custom claim, to be checked in Authorizator method
			return MapClaims{"testkey": "testval", "exp": 0}
		},
		Authenticator: func(c *gin.Context) (interface{}, error) {
			var loginVals Login
			if binderr := c.ShouldBind(&loginVals); binderr != nil {
				return "", ErrMissingLoginValues
			}
			userID := loginVals.Username
			password := loginVals.Password
			if userID == "admin" && password == "admin" {
				return userID, nil
			}
			return "", ErrFailedAuthentication
		},
		Authorizator: func(user interface{}, c *gin.Context) bool {
			return true
		},
		LoginResponse: func(c *gin.Context, code int, token string, refreshToken string, t time.Time) {
			cookie, err := c.Cookie("jwt")
			if err != nil {
				log.Println(err)
			}

			c.JSON(http.StatusOK, gin.H{
				"code":          http.StatusOK,
				"access_token":  token,
				"refresh_token": refreshToken,
				"expire":        t.Format(time.RFC3339),
				"message":       "login successfully",
				"cookie":        cookie,
			})
		},
		SendCookie: true,
	})

	assert.NoError(t, err)

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.POST("/login").
		SetJSON(gofight.D{
			"username": "admin",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			message := gjson.Get(r.Body.String(), "message")

			assert.Equal(t, ErrMissingLoginValues.Error(), message.String())
			assert.Equal(t, http.StatusUnauthorized, r.Code)
			assert.Equal(t, "application/json; charset=utf-8", r.HeaderMap.Get("Content-Type"))
		})

	r.POST("/login").
		SetJSON(gofight.D{
			"username": "admin",
			"password": "test",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			message := gjson.Get(r.Body.String(), "message")
			assert.Equal(t, ErrFailedAuthentication.Error(), message.String())
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.POST("/login").
		SetCookie(gofight.H{
			"jwt": "jwt",
		}).
		SetJSON(gofight.D{
			"username": "admin",
			"password": "admin",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			message := gjson.Get(r.Body.String(), "message")
			cookie := gjson.Get(r.Body.String(), "cookie")
			assert.Equal(t, "login successfully", message.String())
			assert.Equal(t, http.StatusOK, r.Code)
			assert.Equal(t, "jwt", cookie.String())
		})
}

func performRequest(r http.Handler, method, path string, token string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, path, nil)

	if token != "" {
		req.Header.Set("Authorization", token)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}

func TestParseToken(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Test 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS384", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestParseTokenRS256(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:            "test zone",
		Key:              key,
		Timeout:          time.Hour,
		MaxRefresh:       time.Hour * 24,
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "testdata/jwtRS256.key",
		PubKeyFile:       "testdata/jwtRS256.key.pub",
		Authenticator:    defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Test 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS384", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("RS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestRefreshHandlerRS256(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:            "test zone",
		Key:              key,
		Timeout:          time.Hour,
		MaxRefresh:       0,
		SigningAlgorithm: "RS256",
		PrivKeyFile:      "testdata/jwtRS256.key",
		PubKeyFile:       "testdata/jwtRS256.key.pub",
		SendCookie:       true,
		CookieName:       "jwt",
		Authenticator:    defaultAuthenticator,
		RefreshResponse: func(c *gin.Context, code int, token string, refreshToken string, t time.Time) {
			cookie, err := c.Cookie("jwt")
			if err != nil {
				log.Println(err)
			}

			c.JSON(http.StatusOK, gin.H{
				"code":          http.StatusOK,
				"access_token":  token,
				"refresh_token": refreshToken,
				"expire":        t.Format(time.RFC3339),
				"message":       "refresh successfully",
				"cookie":        cookie,
			})
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": "",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": "Test 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": makeTokenString("HS256", "admin", "refresh"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": makeTokenString("RS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": makeTokenString("RS256", "admin", "refresh"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			message := gjson.Get(r.Body.String(), "message")
			refreshToken := gjson.Get(r.Body.String(), "refresh_token")
			accessToken := gjson.Get(r.Body.String(), "access_token")
			assert.Equal(t, "refresh successfully", message.String())
			assert.Equal(t, http.StatusOK, r.Code)
			assert.Equal(t, makeTokenString("RS256", "admin", "access"), accessToken.String())
			assert.Equal(t, makeTokenString("RS256", "admin", "refresh"), refreshToken.String())
		})
}

func TestRefreshHandler(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": "",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": "Test 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": makeTokenString("HS256", "admin", "refresh"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestExpiredTokenOnRefreshHandler(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		Authenticator: defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	token := jwt.New(jwt.GetSigningMethod("HS256"))
	claims := token.Claims.(jwt.MapClaims)
	claims["identity"] = "admin"
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["orig_iat"] = 0
	tokenString, _ := token.SignedString(key)

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": tokenString,
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
}

func TestAuthorizator(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,
		Authorizator: func(data interface{}, c *gin.Context) bool {
			return data.(string) == "admin"
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "test", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusForbidden, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestClaimsDuringAuthorization(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:      "test zone",
		Key:        key,
		Timeout:    time.Hour,
		MaxRefresh: time.Hour * 24,
		PayloadFunc: func(data interface{}) MapClaims {
			if v, ok := data.(MapClaims); ok {
				return v
			}

			if reflect.TypeOf(data).String() != "string" {
				return MapClaims{}
			}

			var testkey string
			switch data.(string) {
			case "admin":
				testkey = "1234"
			case "test":
				testkey = "5678"
			case "Guest":
				testkey = ""
			}
			// Set custom claim, to be checked in Authorizator method
			return MapClaims{"identity": data.(string), "testkey": testkey, "exp": 0}
		},
		Authenticator: func(c *gin.Context) (interface{}, error) {
			var loginVals Login

			if err := c.BindJSON(&loginVals); err != nil {
				return "", ErrMissingLoginValues
			}

			userID := loginVals.Username
			password := loginVals.Password

			if userID == "admin" && password == "admin" {
				return userID, nil
			}

			if userID == "test" && password == "test" {
				return userID, nil
			}

			return "Guest", ErrFailedAuthentication
		},
		Authorizator: func(user interface{}, c *gin.Context) bool {
			jwtClaims := ExtractClaims(c)

			if jwtClaims["identity"] == "administrator" {
				return true
			}

			if jwtClaims["testkey"] == "1234" && jwtClaims["identity"] == "admin" {
				return true
			}

			if jwtClaims["testkey"] == "5678" && jwtClaims["identity"] == "test" {
				return true
			}

			return false
		},
	})

	r := gofight.New()
	handler := ginHandler(authMiddleware)

	userToken, _, _ := authMiddleware.TokenGenerator(MapClaims{
		"identity": "administrator",
		"scope":    "access",
	})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + userToken,
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})

	r.POST("/login").
		SetJSON(gofight.D{
			"username": "admin",
			"password": "admin",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			token := gjson.Get(r.Body.String(), "access_token")
			userToken = token.String()
			assert.Equal(t, http.StatusOK, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + userToken,
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})

	r.POST("/login").
		SetJSON(gofight.D{
			"username": "test",
			"password": "test",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			token := gjson.Get(r.Body.String(), "access_token")
			userToken = token.String()
			assert.Equal(t, http.StatusOK, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + userToken,
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func ConvertClaims(claims MapClaims) map[string]interface{} {
	return map[string]interface{}{}
}

func TestEmptyClaims(t *testing.T) {

	var jwtClaims MapClaims

	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:      "test zone",
		Key:        key,
		Timeout:    time.Hour,
		MaxRefresh: time.Hour * 24,
		Authenticator: func(c *gin.Context) (interface{}, error) {
			var loginVals Login
			userID := loginVals.Username
			password := loginVals.Password

			if userID == "admin" && password == "admin" {
				return "", nil
			}

			if userID == "test" && password == "test" {
				return "Administrator", nil
			}

			return userID, ErrFailedAuthentication
		},
		Unauthorized: func(c *gin.Context, code int, message string) {
			assert.Empty(t, ExtractClaims(c))
			assert.Empty(t, ConvertClaims(ExtractClaims(c)))
			c.String(code, message)
		},
	})

	r := gofight.New()
	handler := ginHandler(authMiddleware)

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	assert.Empty(t, jwtClaims)
}

func TestUnauthorized(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,
		Unauthorized: func(c *gin.Context, code int, message string) {
			c.String(code, message)
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer 1234",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
}

func TestTokenExpire(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    -time.Hour + -time.Second,
		Authenticator: defaultAuthenticator,
		Unauthorized: func(c *gin.Context, code int, message string) {
			c.String(code, message)
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	userToken, _, _ := authMiddleware.TokenGenerator(MapClaims{
		"identity": "admin",
	})

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": userToken,
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
}

func TestDefineTokenHeadName(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		TokenHeadName: "JWTTOKEN       ",
		Authenticator: defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "JWTTOKEN " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestHTTPStatusMessageFunc(t *testing.T) {
	var successError = errors.New("Successful test error")
	var failedError = errors.New("Failed test error")
	var successMessage = "Overwrite error message."

	authMiddleware, _ := New(&GinJWTMiddleware{
		Key:           key,
		Timeout:       time.Hour,
		MaxRefresh:    time.Hour * 24,
		Authenticator: defaultAuthenticator,

		HTTPStatusMessageFunc: func(e error, c *gin.Context) string {
			if e == successError {
				return successMessage
			}

			return e.Error()
		},
	})

	successString := authMiddleware.HTTPStatusMessageFunc(successError, nil)
	failedString := authMiddleware.HTTPStatusMessageFunc(failedError, nil)

	assert.Equal(t, successMessage, successString)
	assert.NotEqual(t, successMessage, failedString)
}

func TestSendAuthorizationBool(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:             "test zone",
		Key:               key,
		Timeout:           time.Hour,
		MaxRefresh:        time.Hour * 24,
		Authenticator:     defaultAuthenticator,
		SendAuthorization: true,
		Authorizator: func(data interface{}, c *gin.Context) bool {
			return data.(string) == "admin"
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "test", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusForbidden, r.Code)
		})

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			token := r.HeaderMap.Get("Authorization")
			assert.Equal(t, "Bearer "+makeTokenString("HS256", "admin", "access"), token)
			assert.Equal(t, http.StatusOK, r.Code)
		})
}

func TestExpiredTokenOnAuth(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:             "test zone",
		Key:               key,
		Timeout:           time.Hour,
		MaxRefresh:        time.Hour * 24,
		Authenticator:     defaultAuthenticator,
		SendAuthorization: true,
		Authorizator: func(data interface{}, c *gin.Context) bool {
			return data.(string) == "admin"
		},
		TimeFunc: func() time.Time {
			return time.Now().AddDate(0, 0, 1)
		},
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.GET("/auth/hello").
		SetHeader(gofight.H{
			"Authorization": "Bearer " + makeTokenString("HS256", "admin", "access"),
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
}

func TestBadTokenOnRefreshHandler(t *testing.T) {
	// the middleware to test
	authMiddleware, _ := New(&GinJWTMiddleware{
		Realm:         "test zone",
		Key:           key,
		Timeout:       time.Hour,
		Authenticator: defaultAuthenticator,
	})

	handler := ginHandler(authMiddleware)

	r := gofight.New()

	r.POST("/auth/refresh_token").
		SetJSON(gofight.D{
			"refresh_token": "BadToken",
		}).
		Run(handler, func(r gofight.HTTPResponse, rq gofight.HTTPRequest) {
			assert.Equal(t, http.StatusUnauthorized, r.Code)
		})
}
