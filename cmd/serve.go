package cmd

import (
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/facebookgo/grace/gracehttp"
	"github.com/golang/glog"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/mobingilabs/mobingi-sdk-go/pkg/private"
	"github.com/mobingilabs/oath/api/v1"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	"github.com/spf13/cobra"
)

var (
	port   string
	region string
	bucket string
)

func ServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run as an http server.",
		Long:  `Run as an http server.`,
		Run:   serve,
	}

	cmd.Flags().SortFlags = false
	cmd.Flags().StringVar(&port, "port", "8080", "server port")
	cmd.Flags().StringVar(&region, "aws-region", "ap-northeast-1", "aws region to access resources")
	cmd.Flags().StringVar(&bucket, "token-bucket", "oath-store", "s3 bucket that contains our key files")
	return cmd
}

func serve(cmd *cobra.Command, args []string) {
	pempub, pemprv, err := downloadTokenFiles()
	if err != nil {
		err = errors.Wrap(err, "download token files failed, fatal")
		glog.Exit(err)
	}

	e := echo.New()

	// time in, should be the first middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cid := uuid.NewV4().String()
			c.Set("contextid", cid)
			c.Set("starttime", time.Now())

			// Helper func to print the elapsed time since this middleware. Good to call at end of
			// request handlers, right before/after replying to caller.
			c.Set("fnelapsed", func(ctx echo.Context) {
				start := ctx.Get("starttime").(time.Time)
				glog.Infof("<-- %v, delta: %v", ctx.Get("contextid"), time.Now().Sub(start))
			})

			glog.Infof("--> %v", cid)
			return next(c)
		}
	})

	e.Use(middleware.CORS())

	// some information about request
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			glog.Infof("remoteaddr: %v", c.Request().RemoteAddr)
			return next(c)
		}
	})

	// add server name in response
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set(echo.HeaderServer, "mobingi:oath:"+version)
			return next(c)
		}
	})

	e.GET("/", func(c echo.Context) error {
		c.String(http.StatusOK, "Copyright (c) Mobingi, 2015-2017. All rights reserved.")
		return nil
	})

	e.GET("/version", func(c echo.Context) error {
		c.String(http.StatusOK, version)
		return nil
	})

	// routes
	v1.NewApiV1(e, &v1.ApiV1Config{
		PublicPemFile:  pempub,
		PrivatePemFile: pemprv,
		AwsRegion:      region,
	})

	// serve
	glog.Infof("serving on :%v", port)
	e.Server.Addr = ":" + port
	gracehttp.Serve(e.Server)
}

func downloadTokenFiles() (string, string, error) {
	var pempub, pemprv string
	var err error

	fnames := []string{"private.key", "public.key"}
	sess := session.Must(session.NewSession())
	svc := s3.New(sess, &aws.Config{
		Region: aws.String(region),
	})

	// create dir if necessary
	tmpdir := os.TempDir() + "/jwt/rsa/"
	if !private.Exists(tmpdir) {
		err := os.MkdirAll(tmpdir, 0700)
		if err != nil {
			err = errors.Wrap(err, "mkdir failed: "+tmpdir)
			glog.Error(err)
			return pempub, pemprv, err
		}
	}

	downloader := s3manager.NewDownloaderWithClient(svc)
	for _, i := range fnames {
		fl := tmpdir + i
		f, err := os.Create(fl)
		if err != nil {
			err = errors.Wrap(err, "create file failed: "+fl)
			glog.Error(err)
			return pempub, pemprv, err
		}

		n, err := downloader.Download(f, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(i),
		})

		if err != nil {
			err = errors.Wrap(err, "s3 download failed: "+fl)
			glog.Error(err)
			return pempub, pemprv, err
		}

		glog.Infof("download s3 file: %s (%v bytes)", i, n)
	}

	pempub = tmpdir + fnames[1]
	pemprv = tmpdir + fnames[0]
	glog.Info(pempub, ", ", pemprv)

	return pempub, pemprv, err
}
