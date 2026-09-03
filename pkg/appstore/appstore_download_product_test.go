package appstore

import (
	"errors"
	gohttp "net/http"

	"github.com/majd/ipatool/v2/pkg/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("AppStore (Download Product)", func() {
	const (
		testGUID      = "001122334455"
		testVersionID = "123456789"
	)

	var (
		ctrl               *gomock.Controller
		mockBagClient      *http.MockClient[bagResult]
		mockDownloadClient *http.MockClient[downloadResult]
		store              *appstore
		account            Account
		app                App
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockBagClient = http.NewMockClient[bagResult](ctrl)
		mockDownloadClient = http.NewMockClient[downloadResult](ctrl)
		store = &appstore{
			bagClient:      mockBagClient,
			downloadClient: mockDownloadClient,
		}
		account = Account{
			DirectoryServicesID: "test-dsid",
			Pod:                 "42",
		}
		app = App{ID: 987654321}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("uses volumeStore as the primary endpoint", func() {
		expected := http.Result[downloadResult]{
			StatusCode: gohttp.StatusOK,
			Data: downloadResult{
				Items: []downloadItemResult{{URL: "https://example.com/app.ipa"}},
			},
		}

		mockDownloadClient.EXPECT().
			Send(gomock.Any()).
			Do(func(req http.Request) {
				Expect(req.URL).To(Equal("https://p42-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/volumeStoreDownloadProduct?guid=" + testGUID))
				Expect(req.Headers).To(HaveKeyWithValue("iCloud-DSID", account.DirectoryServicesID))

				payload, ok := req.Payload.(*http.XMLPayload)
				Expect(ok).To(BeTrue())
				Expect(payload.Content).To(HaveKeyWithValue("externalVersionId", testVersionID))
				Expect(payload.Content).To(HaveKeyWithValue("serialNumber", "0"))
				Expect(payload.Content).ToNot(HaveKey("appExtVrsId"))
			}).
			Return(expected, nil)

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).ToNot(HaveOccurred())
		Expect(actual).To(Equal(expected))
	})

	It("does not fall back for an App Store error response", func() {
		expected := http.Result[downloadResult]{
			StatusCode: gohttp.StatusOK,
			Data: downloadResult{
				FailureType:     FailureTypeLicenseNotFound,
				CustomerMessage: "License not found",
			},
		}

		mockDownloadClient.EXPECT().
			Send(gomock.Any()).
			Return(expected, nil)

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).ToNot(HaveOccurred())
		Expect(actual).To(Equal(expected))
	})

	It("falls back to redownloadProduct for an empty volumeStore response", func() {
		primary := http.Result[downloadResult]{StatusCode: gohttp.StatusOK}
		expected := http.Result[downloadResult]{
			StatusCode: gohttp.StatusOK,
			Data: downloadResult{
				Items: []downloadItemResult{{URL: "https://example.com/redownload.ipa"}},
			},
		}
		bag := validBagResult()
		bag.URLBag.RedownloadEndpoint = testRedownloadEndpoint

		gomock.InOrder(
			mockDownloadClient.EXPECT().
				Send(gomock.Any()).
				Return(primary, nil),
			mockBagClient.EXPECT().
				Send(gomock.Any()).
				Do(func(req http.Request) {
					Expect(req.URL).To(Equal("https://init.itunes.apple.com/bag.xml?guid=" + testGUID))
				}).
				Return(http.Result[bagResult]{StatusCode: gohttp.StatusOK, Data: bag}, nil),
			mockDownloadClient.EXPECT().
				Send(gomock.Any()).
				Do(func(req http.Request) {
					Expect(req.URL).To(Equal(testRedownloadEndpoint + "?guid=" + testGUID))

					payload, ok := req.Payload.(*http.XMLPayload)
					Expect(ok).To(BeTrue())
					Expect(payload.Content).To(HaveKeyWithValue("appExtVrsId", testVersionID))
					Expect(payload.Content).To(HaveKeyWithValue("serialNumber", "0"))
					Expect(payload.Content).ToNot(HaveKey("externalVersionId"))
				}).
				Return(expected, nil),
		)

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).ToNot(HaveOccurred())
		Expect(actual).To(Equal(expected))
	})

	It("preserves the empty response when the bag has no redownload endpoint", func() {
		expected := http.Result[downloadResult]{StatusCode: gohttp.StatusOK}

		mockDownloadClient.EXPECT().
			Send(gomock.Any()).
			Return(expected, nil)
		mockBagClient.EXPECT().
			Send(gomock.Any()).
			Return(http.Result[bagResult]{StatusCode: gohttp.StatusOK, Data: validBagResult()}, nil)

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).ToNot(HaveOccurred())
		Expect(actual).To(Equal(expected))
	})

	It("returns an error when the fallback bag request fails", func() {
		mockDownloadClient.EXPECT().
			Send(gomock.Any()).
			Return(http.Result[downloadResult]{StatusCode: gohttp.StatusOK}, nil)
		mockBagClient.EXPECT().
			Send(gomock.Any()).
			Return(http.Result[bagResult]{}, errors.New("bag request failed"))

		_, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).To(MatchError(ContainSubstring("failed to get bag for redownload fallback")))
	})

	It("returns an error when the redownload request fails", func() {
		bag := validBagResult()
		bag.URLBag.RedownloadEndpoint = testRedownloadEndpoint

		gomock.InOrder(
			mockDownloadClient.EXPECT().
				Send(gomock.Any()).
				Return(http.Result[downloadResult]{StatusCode: gohttp.StatusOK}, nil),
			mockBagClient.EXPECT().
				Send(gomock.Any()).
				Return(http.Result[bagResult]{StatusCode: gohttp.StatusOK, Data: bag}, nil),
			mockDownloadClient.EXPECT().
				Send(gomock.Any()).
				Return(http.Result[downloadResult]{}, errors.New("redownload failed")),
		)

		_, err := store.sendDownloadProduct(account, app, testGUID, testVersionID)
		Expect(err).To(MatchError(ContainSubstring("failed to send redownload request")))
	})

	DescribeTable("validates redownload endpoints",
		func(endpoint string, valid bool) {
			_, err := newRedownloadEndpoint(endpoint)
			if valid {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("valid Apple endpoint", testRedownloadEndpoint, true),
		Entry("non-HTTPS endpoint", "http://downloaddispatch.itunes.apple.com/r/redownload", false),
		Entry("unexpected host", "https://example.com/r/redownload", false),
		Entry("unexpected path", "https://downloaddispatch.itunes.apple.com/r/other", false),
	)
})
