package appstore

import (
	"errors"
	"fmt"
	gohttp "net/http"
	"net/url"
	"strconv"

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
		mockPlatformClient *http.MockClient[platformVersionLookupResult]
		store              *appstore
		account            Account
		app                App
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockBagClient = http.NewMockClient[bagResult](ctrl)
		mockDownloadClient = http.NewMockClient[downloadResult](ctrl)
		mockPlatformClient = http.NewMockClient[platformVersionLookupResult](ctrl)
		store = &appstore{
			bagClient:      mockBagClient,
			downloadClient: mockDownloadClient,
			platformClient: mockPlatformClient,
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

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
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

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
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

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
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

		actual, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
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

		_, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
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

		_, err := store.sendDownloadProduct(account, app, testGUID, testVersionID, PlatformIPhone)
		Expect(err).To(MatchError(ContainSubstring("failed to send redownload request")))
	})

	Describe("empty redownload HTTP 500", func() {
		var latestVersion platformVersionLookupResult

		BeforeEach(func() {
			account.StoreFront = "143441-1,34"
			latestVersion = platformVersionLookupResult{
				Results: map[string]platformVersionLookupItem{
					strconv.FormatInt(app.ID, 10): {
						Offers: []platformVersionLookupOffer{
							{Version: platformVersionLookupVersion{ExternalID: platformVersionExternalID(testVersionID)}},
						},
					},
				},
			}
			bag := validBagResult()
			bag.URLBag.RedownloadEndpoint = testRedownloadEndpoint
			first := mockDownloadClient.EXPECT().Send(gomock.Any()).
				Return(http.Result[downloadResult]{StatusCode: gohttp.StatusOK}, nil)
			mockBagClient.EXPECT().Send(gomock.Any()).After(first).
				Return(http.Result[bagResult]{StatusCode: gohttp.StatusOK, Data: bag}, nil)
		})

		DescribeTable("retries once with the latest catalog version",
			func(platform Platform) {
				empty500 := &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}
				expected := http.Result[downloadResult]{
					StatusCode: gohttp.StatusOK,
					Data:       downloadResult{Items: []downloadItemResult{{URL: "https://example.com/karing.ipa"}}},
				}
				gomock.InOrder(
					mockDownloadClient.EXPECT().Send(gomock.Any()).
						Return(http.Result[downloadResult]{}, fmt.Errorf("wrapped: %w", empty500)),
					mockPlatformClient.EXPECT().Send(gomock.Any()).
						Do(func(req http.Request) {
							u, err := url.Parse(req.URL)
							Expect(err).ToNot(HaveOccurred())
							Expect(u.Host).To(Equal("uclient-api.itunes.apple.com"))
							Expect(u.Query().Get("id")).To(Equal(strconv.FormatInt(app.ID, 10)))
							Expect(u.Query().Get("cc")).To(Equal("us"))
							Expect(u.Query().Get("platform")).To(Equal("enterprisestore"))
						}).
						Return(http.Result[platformVersionLookupResult]{StatusCode: gohttp.StatusOK, Data: latestVersion}, nil),
					mockDownloadClient.EXPECT().Send(gomock.Any()).
						Do(func(req http.Request) {
							Expect(req.URL).To(Equal(testRedownloadEndpoint + "?guid=" + testGUID))
							Expect(req.Method).To(Equal(http.MethodPOST))
							payload := req.Payload.(*http.XMLPayload).Content
							Expect(payload).To(HaveKeyWithValue("salableAdamId", app.ID))
							Expect(payload).To(HaveKeyWithValue("guid", testGUID))
							Expect(payload).To(HaveKeyWithValue("appExtVrsId", testVersionID))
							Expect(payload).ToNot(HaveKey("externalVersionId"))
							Expect(payload).ToNot(HaveKey("pricingParameters"))
						}).Return(expected, nil),
				)
				actual, err := store.sendDownloadProduct(account, app, testGUID, "", platform)
				Expect(err).ToNot(HaveOccurred())
				Expect(actual).To(Equal(expected))
			},
			Entry("default iOS platform", Platform("")),
			Entry("iPhone", PlatformIPhone),
			Entry("iPad", PlatformIPad),
		)

		DescribeTable("does not retry unrelated redownload errors",
			func(original error) {
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, original)
				_, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
				Expect(errors.Is(err, original)).To(BeTrue())
			},
			Entry("authentication", &http.UnexpectedResponseError{StatusCode: gohttp.StatusForbidden}),
			Entry("rate limit", &http.UnexpectedResponseError{StatusCode: gohttp.StatusTooManyRequests}),
			Entry("service unavailable", &http.UnexpectedResponseError{StatusCode: gohttp.StatusServiceUnavailable}),
			Entry("nonempty 500 message", &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError, Snippet: "maintenance"}),
			Entry("network failure", errors.New("connection reset")),
		)

		DescribeTable("does not replace an explicit version or cross platforms",
			func(versionID string, platform Platform) {
				empty500 := &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, empty500)
				_, err := store.sendDownloadProduct(account, app, testGUID, versionID, platform)
				Expect(errors.Is(err, empty500)).To(BeTrue())
			},
			Entry("explicit version", testVersionID, PlatformIPhone),
			Entry("macOS", "", PlatformMacOS),
			Entry("tvOS", "", PlatformAppleTV),
			Entry("visionOS", "", PlatformVisionOS),
		)

		It("preserves an actionable failure from the pinned retry", func() {
			expected := http.Result[downloadResult]{StatusCode: gohttp.StatusOK,
				Data: downloadResult{FailureType: FailureTypeLicenseNotFound}}
			gomock.InOrder(
				mockDownloadClient.EXPECT().Send(gomock.Any()).
					Return(http.Result[downloadResult]{}, &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}),
				mockPlatformClient.EXPECT().Send(gomock.Any()).
					Return(http.Result[platformVersionLookupResult]{StatusCode: gohttp.StatusOK, Data: latestVersion}, nil),
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(expected, nil),
			)
			actual, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(Equal(expected))
		})

		It("does not loop when the pinned retry also returns HTTP 500", func() {
			empty500 := &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}
			gomock.InOrder(
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, empty500),
				mockPlatformClient.EXPECT().Send(gomock.Any()).
					Return(http.Result[platformVersionLookupResult]{StatusCode: gohttp.StatusOK, Data: latestVersion}, nil),
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, empty500),
			)
			_, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
			Expect(errors.Is(err, empty500)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("failed to send version-pinned redownload request")))
		})

		It("preserves the original error when catalog lookup fails", func() {
			empty500 := &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}
			lookupErr := errors.New("catalog unavailable")
			gomock.InOrder(
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, empty500),
				mockPlatformClient.EXPECT().Send(gomock.Any()).Return(http.Result[platformVersionLookupResult]{}, lookupErr),
			)
			_, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
			Expect(errors.Is(err, empty500)).To(BeTrue())
			Expect(errors.Is(err, lookupErr)).To(BeTrue())
		})

		It("does not retry without a catalog version", func() {
			empty500 := &http.UnexpectedResponseError{StatusCode: gohttp.StatusInternalServerError}
			gomock.InOrder(
				mockDownloadClient.EXPECT().Send(gomock.Any()).Return(http.Result[downloadResult]{}, empty500),
				mockPlatformClient.EXPECT().Send(gomock.Any()).
					Return(http.Result[platformVersionLookupResult]{StatusCode: gohttp.StatusOK}, nil),
			)
			_, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
			Expect(errors.Is(err, empty500)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("platform version lookup returned no app")))
		})

		It("does not apply the retry to a plist response", func() {
			expected := http.Result[downloadResult]{StatusCode: gohttp.StatusInternalServerError}
			mockDownloadClient.EXPECT().Send(gomock.Any()).Return(expected, nil)
			actual, err := store.sendDownloadProduct(account, app, testGUID, "", PlatformIPhone)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(Equal(expected))
		})
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
