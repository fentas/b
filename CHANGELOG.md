# Changelog

## [4.1.0](https://github.com/fentas/b/compare/v4.0.0...v4.1.0) (2025-08-21)


### Features

* add kubelogin binary support for Kubernetes OpenID Connect authentication ([#74](https://github.com/fentas/b/issues/74)) ([5dfdde6](https://github.com/fentas/b/commit/5dfdde600fb2eb7c538ca862c8b48bf0fbfd3db8))

## [4.0.0](https://github.com/fentas/b/compare/v3.0.1...v4.0.0) (2025-08-19)


### ⚠ BREAKING CHANGES

* initialize documentation site and restructure config yaml ([#59](https://github.com/fentas/b/issues/59))
* new cli ([#50](https://github.com/fentas/b/issues/50))
* change bin names to params not flags ([#4](https://github.com/fentas/b/issues/4))

### Features

* add b binary (selfupdate) ([#10](https://github.com/fentas/b/issues/10)) ([b6ab725](https://github.com/fentas/b/commit/b6ab725b2b4278770b3eda607d95407eac9f697c))
* add binary alias support with renvsubst implementation ([#72](https://github.com/fentas/b/issues/72)) ([06f43b4](https://github.com/fentas/b/commit/06f43b43adcf8f1cadd4a63e3e4a3120e50986a4))
* add clusterctl ([#23](https://github.com/fentas/b/issues/23)) ([f6949ba](https://github.com/fentas/b/commit/f6949ba015e13d52fc1e4a12dfa6324c92d3c3f9))
* add curl binary support with tar.xz extraction capability ([#52](https://github.com/fentas/b/issues/52)) ([9d2dc57](https://github.com/fentas/b/commit/9d2dc5777a2faf12d74a49013bc697969cadc735))
* add docker-compose ([#8](https://github.com/fentas/b/issues/8)) ([c3f91f9](https://github.com/fentas/b/commit/c3f91f94278282af7f1931836d8ab4ec539845d5))
* add dynamic file extension detection and cilium binary support ([#54](https://github.com/fentas/b/issues/54)) ([73e87ba](https://github.com/fentas/b/commit/73e87ba4e7eddbcf87909ae682ba524be2f6065a))
* add gh ([#7](https://github.com/fentas/b/issues/7)) ([81e9e03](https://github.com/fentas/b/commit/81e9e03b0e3d3f10f51702f18d368944c7a3d527))
* add hubble binary support with GitHub release integration ([#57](https://github.com/fentas/b/issues/57)) ([63084dc](https://github.com/fentas/b/commit/63084dc7b2c7f5d9268bcc27cc66857c93317d03))
* add kubeseal ([#46](https://github.com/fentas/b/issues/46)) ([6c313e3](https://github.com/fentas/b/commit/6c313e3aba68eeb810cfade15cf0efdadc36819c))
* add kustomize ([#35](https://github.com/fentas/b/issues/35)) ([99fc757](https://github.com/fentas/b/commit/99fc757fb057aa00eb005f8723e1432aa29859cb))
* add packer and zip support ([#43](https://github.com/fentas/b/issues/43)) ([664a657](https://github.com/fentas/b/commit/664a657140dca269fa34e0a67fc4b975054b6b12))
* add sops ([#40](https://github.com/fentas/b/issues/40)) ([35356d4](https://github.com/fentas/b/commit/35356d4a1d9a34c81b1924dea9f342dc75c67c0c))
* add state and change --all behaviour ([edf3100](https://github.com/fentas/b/commit/edf3100a1f696879acc6a8318452ee013bf9fcd5))
* add stern ([#38](https://github.com/fentas/b/issues/38)) ([670894a](https://github.com/fentas/b/commit/670894abfb71edb44759124c198731be1ebb7c4f))
* change bin names to params not flags ([#4](https://github.com/fentas/b/issues/4)) ([3f12359](https://github.com/fentas/b/commit/3f12359017b49fb1bbbd4e7168f43d938fcf5aac))
* fix release-please version management for 1.1.2 ([#14](https://github.com/fentas/b/issues/14)) ([74bb689](https://github.com/fentas/b/commit/74bb689017d3abd380c369c9d9f8f7d82dfc565c))
* initialize documentation site and restructure config yaml ([#59](https://github.com/fentas/b/issues/59)) ([20dc4e3](https://github.com/fentas/b/commit/20dc4e3ef22f49136a037b1dce4bfb55b8cf8b41))
* Make config optional ([#3](https://github.com/fentas/b/issues/3)) ([20d7875](https://github.com/fentas/b/commit/20d7875284866f198680234622e6fdb9c58cc10e))
* update GPG signing configuration ([#19](https://github.com/fentas/b/issues/19)) ([06cea06](https://github.com/fentas/b/commit/06cea0612365c2fbbf4657d9ce1be4571a8fc934))
* update release-please to use bot token for workflow triggering ([#21](https://github.com/fentas/b/issues/21)) ([5bb0957](https://github.com/fentas/b/commit/5bb0957470b001922c9b62141f284e2a598e0f81))


### Bug Fixes

* add argsh binary ([#5](https://github.com/fentas/b/issues/5)) ([0262cfc](https://github.com/fentas/b/commit/0262cfc6b220d7765b4b6e268f4eabd66dca2955))
* add argsh to readme ([090a9ba](https://github.com/fentas/b/commit/090a9ba6ac84da330cf8bdaa32029417ba391ac8))
* Add progress tracking for zip extraction ([#48](https://github.com/fentas/b/issues/48)) ([f6749a4](https://github.com/fentas/b/commit/f6749a47712c3317e67a32e97de811d1415b47e8))
* argsh wrong repo ([#68](https://github.com/fentas/b/issues/68)) ([d860d91](https://github.com/fentas/b/commit/d860d91e0510c57e9d11780c346663be7ba1c886))
* clusterctl version ([#31](https://github.com/fentas/b/issues/31)) ([87fbb6a](https://github.com/fentas/b/commit/87fbb6ab8142939ea3697b82a2d2db94e50e624a))
* NoConfig logic wrong ([#11](https://github.com/fentas/b/issues/11)) ([3fc224d](https://github.com/fentas/b/commit/3fc224d9c3f99962fc86d9503644181a17149f61))
* quiet mode ([#33](https://github.com/fentas/b/issues/33)) ([4de443c](https://github.com/fentas/b/commit/4de443cc622c41f3b3dd457edbc158ee5072885d))
* quiet mode and clusterctl version check ([#25](https://github.com/fentas/b/issues/25)) ([d9d5115](https://github.com/fentas/b/commit/d9d5115de46bcc61cb336bba0c71dd38add4d246))


### Code Refactoring

* new cli ([#50](https://github.com/fentas/b/issues/50)) ([eecf81b](https://github.com/fentas/b/commit/eecf81bb04852cb154b6557b09272af87ab25401))

## [3.0.1](https://github.com/fentas/b/compare/v3.0.0...v3.0.1) (2025-08-17)


### Bug Fixes

* argsh wrong repo ([#68](https://github.com/fentas/b/issues/68)) ([55f5c79](https://github.com/fentas/b/commit/55f5c79b61ba44b4a73d70fc036c2acf297501ca))

## [3.0.0](https://github.com/fentas/b/compare/v2.3.0...v3.0.0) (2025-08-07)


### ⚠ BREAKING CHANGES

* initialize documentation site and restructure config yaml ([#59](https://github.com/fentas/b/issues/59))

### Features

* initialize documentation site and restructure config yaml ([#59](https://github.com/fentas/b/issues/59)) ([97fd28c](https://github.com/fentas/b/commit/97fd28ce9595d1f1a1380b0650c8a8b875c7796c))

## [2.3.0](https://github.com/fentas/b/compare/v2.2.0...v2.3.0) (2025-08-07)


### Features

* add hubble binary support with GitHub release integration ([#57](https://github.com/fentas/b/issues/57)) ([c95d52d](https://github.com/fentas/b/commit/c95d52d6bf9b3cc9ef99ac4f892c89e568cf962b))

## [2.2.0](https://github.com/fentas/b/compare/v2.1.0...v2.2.0) (2025-08-07)


### Features

* add dynamic file extension detection and cilium binary support ([#54](https://github.com/fentas/b/issues/54)) ([c504a7c](https://github.com/fentas/b/commit/c504a7c5932cb4c1b84a54b9e40528932d04e2a3))

## [2.1.0](https://github.com/fentas/b/compare/v2.0.0...v2.1.0) (2025-08-07)


### Features

* add curl binary support with tar.xz extraction capability ([#52](https://github.com/fentas/b/issues/52)) ([51d7c4b](https://github.com/fentas/b/commit/51d7c4b69079e121b9dd69af65129d3feeceb209))

## [2.0.0](https://github.com/fentas/b/compare/v1.10.1...v2.0.0) (2025-08-06)


### ⚠ BREAKING CHANGES

* new cli ([#50](https://github.com/fentas/b/issues/50))

### Code Refactoring

* new cli ([#50](https://github.com/fentas/b/issues/50)) ([ce3fcce](https://github.com/fentas/b/commit/ce3fcce2b56daee53601f10ae969a8c9acb981e6))

## [1.10.1](https://github.com/fentas/b/compare/v1.10.0...v1.10.1) (2025-08-06)


### Bug Fixes

* Add progress tracking for zip extraction ([#48](https://github.com/fentas/b/issues/48)) ([13cb0bc](https://github.com/fentas/b/commit/13cb0bcfa7367425e46a6dc61708b45ca0d48f6d))

## [1.10.0](https://github.com/fentas/b/compare/v1.9.0...v1.10.0) (2025-07-16)


### Features

* add kubeseal ([#46](https://github.com/fentas/b/issues/46)) ([b993b31](https://github.com/fentas/b/commit/b993b313900d228c538bccc088eaefd55e1a8095))

## [1.9.0](https://github.com/fentas/b/compare/v1.8.0...v1.9.0) (2025-07-15)


### Features

* add packer and zip support ([#43](https://github.com/fentas/b/issues/43)) ([2c06fa8](https://github.com/fentas/b/commit/2c06fa85f9a971f3d3453b8b85f7aec58162abeb))

## [1.8.0](https://github.com/fentas/b/compare/v1.7.0...v1.8.0) (2025-07-13)


### Features

* add sops ([#40](https://github.com/fentas/b/issues/40)) ([c5203cb](https://github.com/fentas/b/commit/c5203cbb3645743542604ab66f6cf60819e7d362))

## [1.7.0](https://github.com/fentas/b/compare/v1.6.0...v1.7.0) (2025-07-11)


### Features

* add stern ([#38](https://github.com/fentas/b/issues/38)) ([6aaca43](https://github.com/fentas/b/commit/6aaca434776df7a25cad70c39374b7102877cc7c))

## [1.6.0](https://github.com/fentas/b/compare/v1.5.3...v1.6.0) (2025-07-09)


### Features

* add kustomize ([#35](https://github.com/fentas/b/issues/35)) ([71b176e](https://github.com/fentas/b/commit/71b176ef4eabfb62e0abbe751eea52298ad66557))

## [1.5.3](https://github.com/fentas/b/compare/v1.5.2...v1.5.3) (2025-07-09)


### Bug Fixes

* quiet mode ([#33](https://github.com/fentas/b/issues/33)) ([919a5b6](https://github.com/fentas/b/commit/919a5b68d5e43c5e4cda176cf5ef149a42509bb7))

## [1.5.2](https://github.com/fentas/b/compare/v1.5.1...v1.5.2) (2025-07-09)


### Bug Fixes

* clusterctl version ([#31](https://github.com/fentas/b/issues/31)) ([7cf5016](https://github.com/fentas/b/commit/7cf5016cfe9dbfcb84c6ce34c91ba00818140e66))

## [1.5.1](https://github.com/fentas/b/compare/v1.5.0...v1.5.1) (2025-07-08)


### Bug Fixes

* quiet mode and clusterctl version check ([#25](https://github.com/fentas/b/issues/25)) ([a7ad845](https://github.com/fentas/b/commit/a7ad845ef1de6f4a9e3a2c9f7a92d3cd9c102435))

## [1.5.0](https://github.com/fentas/b/compare/v1.4.0...v1.5.0) (2025-07-08)


### Features

* add clusterctl ([#23](https://github.com/fentas/b/issues/23)) ([274cbb2](https://github.com/fentas/b/commit/274cbb285b0d82a5c096476ecbac6e5cab821512))

## [1.4.0](https://github.com/fentas/b/compare/v1.3.0...v1.4.0) (2025-06-28)


### Features

* update release-please to use bot token for workflow triggering ([#21](https://github.com/fentas/b/issues/21)) ([2292a53](https://github.com/fentas/b/commit/2292a532a32776b8ac61fb4241447811590a66ba))

## [1.3.0](https://github.com/fentas/b/compare/v1.2.0...v1.3.0) (2025-06-28)


### Features

* update GPG signing configuration ([#19](https://github.com/fentas/b/issues/19)) ([7efb8c9](https://github.com/fentas/b/commit/7efb8c9c5f2fafb468e9c0366ca3feb898b43611))

## [1.2.0](https://github.com/fentas/b/compare/v1.1.1...v1.2.0) (2025-06-28)


### Features

* fix release-please version management for 1.1.2 ([#14](https://github.com/fentas/b/issues/14)) ([5071cbb](https://github.com/fentas/b/commit/5071cbbf4386ffb3880a0c74e847713b9577bc54))

## [1.0.0](https://github.com/buyoio/b/compare/v0.1.0...v1.0.0) (2024-04-03)


### ⚠ BREAKING CHANGES

* change bin names to params not flags ([#4](https://github.com/buyoio/b/issues/4))

### Features

* add docker-compose ([#8](https://github.com/buyoio/b/issues/8)) ([7e9cfee](https://github.com/buyoio/b/commit/7e9cfeee41b9eaa03b2e7d816f076bb32fc49d75))
* add gh ([#7](https://github.com/buyoio/b/issues/7)) ([6a9d2e0](https://github.com/buyoio/b/commit/6a9d2e028632b1bbb6f04911ca7870ac51ca3097))
* change bin names to params not flags ([#4](https://github.com/buyoio/b/issues/4)) ([a2a4663](https://github.com/buyoio/b/commit/a2a4663b767bffd38a5b61d8a1c490183a949e4a))
* Make config optional ([#3](https://github.com/buyoio/b/issues/3)) ([2c56b60](https://github.com/buyoio/b/commit/2c56b60c9e51d8df74a559033076ae8a60ff1f02))


### Bug Fixes

* add argsh binary ([#5](https://github.com/buyoio/b/issues/5)) ([36cd1cf](https://github.com/buyoio/b/commit/36cd1cf127f5976c1269be851d064c17a497a16a))
* add argsh to readme ([4541d26](https://github.com/buyoio/b/commit/4541d2612274b637df928ce1f9e497583358d121))

## [0.1.0](https://github.com/buyoio/b/compare/v0.0.1...v0.1.0) (2024-03-27)


### Features

* add state and change --all behaviour ([cc291d1](https://github.com/buyoio/b/commit/cc291d140b4ba9007f91fa2114c5964582cd8f75))
