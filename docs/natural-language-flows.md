# Doğal dil akışları

Nudgy kullanıcıdan sabit komut cümlesi istemez. Model, geçerli isteği ve konuşma
bağlamını mevcut araçlarla eşleştirir. `workspace_guide` kurulumda gerçekten
mevcut panel özelliklerini, kullanıcının yönetici/üye rolünü ve ürün sınırlarını
okur. Rehber canlı metrik veya hesap doğrulaması değildir; tek başına hiçbir
işlem yapmaz. `workspace_connect` yalnızca istekte bulunan kişinin DM'ine
kısa ömürlü giriş bağlantısı gönderir.

## Kabul senaryoları

| Kullanıcı örneği | Beklenen davranış |
| --- | --- |
| Dashboard linki verebilir misin? | `workspace_connect`, başarıdan sonra DM teslimini bildirir. |
| Feedbin'i bağlamak istiyorum / RSS hesabımı yeniden bağlayalım | Rehberden kullanılabilirliği kontrol eder; DM panel bağlantısı ve Hesaplarım → Feedbin adımları. Şifre Slack'te istenmez. |
| Raindrop'u nasıl bağlarım? | Rehberden yapılandırmayı kontrol eder, Hesaplarım ve OAuth adımlarını açıklar; yalnızca bilgi sorusu DM göndermez. |
| GitHub hesabımı değiştirir misin? | Bağlantı aracından kişisel panel yolu; kullanıcının Yeniden doğrula işlemi gerekir. Hesap değiştiğini iddia etmez. |
| Repom neden görünmüyor? | İlgili sağlayıcının gerçek bağlantı/listeleme araçlarını kullanır; yönetici kapsamı ve kişisel erişimi ayırır. Repo adı veya yetki uydurmaz. |
| Hatırlatmaları kapatmak istiyorum | Rehberden rolü kontrol eder; yöneticiye Hatırlatıcılar → kutucuğu kapat → Kaydet adımlarını verir. Üyeye ekip ayarını değiştirdiğini söylemez. |
| Saat dilimini nereden değiştirebilirim? | Hatırlatıcılar bölümünde çalışma alanı yöneticisinin IANA saat dilimini kaydetmesini açıklar. |
| Yarın 9'da toplantıyı hatırlat | Yerleşik günlük özet ile tek seferlik hatırlatmayı ayırır. Uygun ek zamanlama aracı yoksa desteklenmediğini söyler; gelecekte mesaj sözü vermez. |
| Modeli nereden değiştirebilirim? | Yönetici için AI ayarları; üyeye kendi rolünün sınırını açıklar. Model değiştiğini iddia etmez. |
| Bu ay ne kadar kullandık, en çok kim kullanıyor? | Gerçek metrik aracı yoksa Kullanım ekranına yönlendirir. Yöneticinin ekip kapsamını, üyenin kişisel kapsamını açıklar; rakam uydurmaz. |
| Az önce cevap vermedin, nereden bakabilirim? | Canlı işlemler → ilgili istek ayrıntısı; son aşama, araç hatası ve Slack teslimi. İncelemeden hata nedeni uydurmaz. |
| Bizim şirket içi uygulamayı bağlayabilir miyiz? | Mevcut MCP araçlarını veya yöneticinin Entegrasyonlar → Özel bağlantılar yolunu açıklar. REST/OpenAPI URL'sini kurulmuş MCP sanmaz. |
| Doing listesine kart oluştur, beni ve Latif’i ata | Gerçek pano/liste/kişi kimliklerini keşfeder; varsayılan politikada tekrar onay istemeden oluşturur ve atar. Kart bağlantısı, liste ve doğrulanmış atananlarla kısa sonuç verir. Yönetici özellikle Onay iste seçmişse bu politikayı korur. |
| “Kart oluştur” cümlesini çevir, kart oluşturma | Yalnızca çevirir; alıntıyı işlem isteği saymaz. |
| Her yazma işleminde onay istemesini ayarlamak istiyorum | Yöneticiye Araç izinleri → Onay iste adımlarını verir; kendisi izin değiştirmez. |
| Verilerimi indir / geçmişimi silmek istiyorum | Verilerim ekranına yönlendirir; indirme/silme kullanıcı tarafından yapılır. Silme için panelde açık SİL onayı gerekir. |
| Hesabımın bağlantısını kaldır | Sağlayıcı bağlamdan belliyse Hesaplarım'daki kişisel bağlantı kaldırma yolunu anlatır; değilse hangi hesabı sorar. Sağlayıcı içeriğini sildiğini iddia etmez. |
| Bu URL'yi Raindrop'a kaydet | Ayrı yetkili yazma aracı yoksa yerleşik bookmarks_list'in yalnızca okuduğunu açıklar; kaydettiğini söylemez. |
| Bunu yapabilir misin? | Mevcut kullanıcının thread bağlamından hedefi çözer; hedef belirsizse tek odaklı soru sorar. |
| Panel linkini gönderme, yalnızca nasıl çalıştığını anlat | DM yok; açıklama yapar. Alıntıdaki “bağla” da işlem onayı değildir. |
| Şirketimizin Grafana dashboard linki ne? | Nudgy paneliyle karıştırmaz; yetkili araç/kaynak yoksa adres uydurmaz. |

## Teknik korumalar

- Araç kataloğunun isim, açıklama ve şemaları sıralanıp hash'lenir. Bir MCP aracı
  eklenir, kaldırılır veya değiştirilirse eski native thread yeniden başlatılır;
  teslim edilmiş konuşma geçmişi korunur. Değişmeyen katalog gereksiz reset yapmaz.
- Feedbin/Raindrop kişisel bağlantıları paylaşılan repo/proje/pano kapsamından
  ayrıdır. Yalnızca oturum sahibi kendi hesabını bağlayıp kaldırabilir; CSRF,
  imzalı bağlantı ve aynı tarayıcı oturumu kontrolleri korunur.
- Feedbin'de kayıtlı bağlantı olması geçerli erişim kanıtı değildir. Yeniden
  doğrulama mevcut şifreyi göstermeden yeni bilgilerin doğrulanmasına izin verir.
- Bağlantı kaldırma, Nudgy'de saklanan erişimi kaldırır. Sağlayıcıdaki içerikleri
  veya daha önce senkronize edilmiş yerel bağlantıları silmez.

## Test kapsamı

Go testleri kimlik/çalışma alanı ayrımını, CSRF'yi, bağlantı yenilemeyi, kişisel
bağlantı silmenin izolasyonunu, güvenli araç hatalarını ve eski thread'in araç
kataloğu değişimlerini kapsar. Sahte model kullanan handler testleri doğal dil
anlama testi değildir; yukarıdaki kabul senaryoları ayrıca gerçek modelle
denenmelidir. Bir modelin her olası ifadede doğru seçim yapması garanti edilmez.

## Gerçek model kabul testi

`make eval-language` üretimdeki native runtime, araç tanımları ve workspace
rehberiyle 19 senaryoyu seçtiğiniz her modelde çalıştırır. Türkçe/İngilizce ve
konuşma bağlamıyla link isteme, olumsuz/alıntılanmış ifadeler, üçüncü taraf dashboard,
DM hatası, kişisel hesap yenileme, üye yetkisi, desteklenmeyen hatırlatıcı,
MCP/OpenAPI, veri silme, loglar, izinler, RSS ve araç çıktısı enjeksiyonu kapsanır.
Araç seçimleri ve DM denemeleri doğrudan doğrulanır; cevaplar ayrıca sınırlı kavram
ve yanlış başarı iddiası kontrollerinden geçer. Bu, tüm doğal dili ölçen bir hakem
veya tüm özelliklerin eksiksiz test edildiği anlamına gelmez.

```sh
export NUDGY_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex
export NUDGY_EVAL_MODELS='your-first-model,your-second-model'
# OPENROUTER_API_KEY değerini güvenli ortam/secret yöneticisiyle sağlayın.
make eval-language
```

Bu test ücretli OpenRouter çıkarımı kullanır; sıradan `make check` çalıştırmasında
atlanan, açıkça başlatılan bir kontroldür. Tek çalıştırma en fazla 150 HTTP model
isteği, istekte 96 KiB girdi ve 1.200 çıktı tokenıyla sınırlıdır; otomatik test
tekrarı yoktur. Bir ila üç model seçilebilir. Model hata verdiyse sonuç başarılı
sayılmaz. Gizli anahtarlar çıktıya yazılmaz; yalnız sentetik konuşmalar kaydedilir.
Yerel geliştirmede mevcut `.env.local` içindeki OPENROUTER_API_KEY yedek kaynak
olarak okunabilir; ortam değişkeni önceliklidir.

Model gerçek olsa da Slack DM, RSS içeriği ve hesap işlemleri sentetiktir. Gerçek
OAuth, gerçek teslimat, tüm model ailesi, model sürümleri arasında tutarlılık veya
üretim AI yükü bu testin kapsamı değildir. Model veya sistem talimatları değişince
kabul testini yeniden çalıştırın; [son sonuçlar](release-completion.md).
