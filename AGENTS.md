# AGENTS.md

このファイルは、Flexible RAID の実装・変更時に守る開発ルールを定義する。
システムの目的、仕様、アーキテクチャ上の責務は README.md を正とし、このファイルでは主にソースコードの構成と実装上の規約を定める。

README.md とこのファイルの内容が矛盾する場合は、仕様・設計については README.md を優先する。未決事項を実装都合で独自に確定せず、必要に応じてペンディングとして明示する。

## 1. 基本方針

- 各レイヤーの責務を守り、他レイヤーの内部実装に依存しない。
- 上位レイヤーから下位レイヤーを利用するときは、下位レイヤーが公開する API を通じて行う。
- レイヤー間の依存関係やデータの受け渡しは、必要な範囲で明示する。
- 意味のない抽象化や、将来の可能性だけを理由とした過剰な汎用化は行わない。
- 仕様や設計で未確定の事項を、実装時に暗黙の前提として固定しない。

## 2. ディレクトリ構成

各レイヤーは独立した Go パッケージとする。

```text
io/
distribution/
orchestration/
fuse/
```

- レイヤーごとのディレクトリをパッケージ境界とする。
- 各レイヤーのソースコードは、そのレイヤーのディレクトリ内に配置する。
- ディレクトリやサブパッケージは、明確な責務上の理由がある場合に追加する。
- ファイルは、操作または機能・責務ごとに分類する。

## 3. ファイル名

- ファイル名はすべて小文字とする。
- 単語の区切りには必要に応じてアンダースコアを使用する。
- 公開・非公開をファイル名で区別しない。Go の公開範囲は識別子の命名規則で決まる。
- ファイル名は、そのファイルが扱う操作または機能・責務を表すものにする。

例:

- `read.go`
- `write.go`
- `recovery.go`
- `metadata.go`
- `mapping.go`

## 4. 操作と実装ファイル

- 外部に提供する操作は、操作ごとに個別の関数として定義する。
- 複数の操作を単一の `Execute` 関数などで振り分ける構成にはしない。
- 操作ごとにソースファイルを分け、そのファイルに原則として次を配置する。
  - その操作の Request 型
  - その操作の Response 型
  - その操作を実行する関数
- 操作にのみ使用する補助関数は、その操作のファイル内に配置する。
- 公開する操作関数は、各操作の実装ファイルで直接定義する。Go のエクスポート規則に従い、その関数自体をレイヤーの公開 API とする。
- 公開関数を `commands.go` などに集約しない。公開操作を列挙するためだけの interface や、実装と重複する関数定義も設けない。
- 操作の追加・変更時は、その操作のファイルを中心に変更できる構成にする。

例:

- `read.go` に `ReadRequest`、`ReadResponse`、`Read` を定義する。
- `write.go` に `WriteRequest`、`WriteResponse`、`Write` を定義する。

## 5. 型

- 公開識別子は PascalCase、非公開識別子は camelCase とする。
- Request 型と Response 型は、原則として操作ごと・レイヤーごとに定義する。
- 操作名が同じであっても、異なるレイヤー間で Request 型や Response 型を安易に共有しない。
- 複数の操作で共有する型は、共有する責務が明確なファイルに配置する。
- 型の共有は重複を避けることだけを目的に行わず、各レイヤーの独立性と責務を優先する。
- 複数レイヤーで共通して使用する型は、共通の責務を持つ `pkg/shared` に定義する。
- 各レイヤーから共通型を使用するときは、`shared.ObjectID` のように `pkg/shared` を直接参照する。
- 共通型を各レイヤーのパッケージ内で型エイリアスとして再公開したり、同じ意味の型を重複して定義したりしない。
- そのレイヤー固有の責務や意味を持つ型は、共通化せず各レイヤーに定義する。

### ObjectID と ParentID

- `ObjectID` は常に有効な UUIDv4 とする。無効値や未設定値を表す ObjectID は設けない。
- ObjectID の生成・文字列解析・バイナリ解析など、外部入力を受け取る境界で UUIDv4 を検証する。検証ロジックは共通の単一の検証経路に集約する。
- ObjectID のゼロ値を特別な識別子、root、未設定値、無効値などとして扱わない。ゼロ値判定やゼロ値を利用した分岐を設けない。
- `ParentID` は nullable とする。通常の Namespace Object は有効な UUIDv4 の親 ID を持つが、root Directory と Namespace 外の HardLinkedFile は `nil` を持つ。
- root 自身も通常の Object として有効な ObjectID を持つ。root は ParentID が nil の唯一の Directory Object として識別し、root の子の ParentID には root 自身の ObjectID を設定する。
- HardLinkedFile も ParentID が nil になるため、ParentID の nil だけで root と判定してはならない。必ず Kind と組み合わせて判定する。
- nullable な識別子を永続化するときは、ゼロ UUID を null の代用にせず、存在フラグなどで有無を明示する。存在する場合の ID は必ず UUIDv4 として検証する。

### ObjectKind、Namespace、HardLink

- 論理 Object の Kind は Orchestration が所有する。Kind は File、Directory、Symlink、HardLinkedFile、HardLink を区別する。
- File は ParentID を持ち、実データを保持する。
- Directory は root 以外 ParentID を持つ。Directory の children は永続化せず、起動時に各 Object の ParentID と Name から Orchestration のメモリ上に構築する。
- Symlink は ParentID を持つ。リンク先パス文字列は Header ではなく通常の論理データとして保持する。
- HardLinkedFile は Namespace ツリー外の実体であり、ParentID と TargetID を持たず、実データを保持する。
- HardLink は Namespace 上の名前を表す Object であり、ParentID と TargetID を持つ。TargetID は必ず HardLinkedFile の ObjectID を直接指し、HardLink は実データを保持しない。
- Header で追加する参照情報は nullable な TargetID のみとし、TargetID は HardLink の場合だけ設定する。HardLinkedFile を識別する追加フラグは設けない。TargetID が存在する場合は UUIDv4 として検証し、nil はゼロ UUID ではなく明示的な存在情報で表す。
- リリースまでは、Header のバイナリ構造を変更しても `FormatVersion` は `0` のままとし、後方互換性の維持や旧形式の読み込み対応は行わない。`HeaderSize` は現在の構造に合わせて更新する。
- HardLink の連鎖、Directory / Symlink を参照先とする HardLink、Directory 自体のハードリンクは認めない。HardLink は Directory ではなく、children を持つ親にはなれない。
- ハードリンク作成時は、元の File の実体を HardLinkedFile として Namespace から切り離し、元の名前と新しい名前をそれぞれ HardLink Object として表現する。同じ HardLinkedFile を複数の HardLink が参照できる。
- 永続化時は実体と各 Namespace Object を個別の Object として保存する。メモリ展開時は Directory の children を構築し、各 HardLink Node が同じ Runtime ファイル実体を共有するように解決する。
- データ実体の Distribution 上の冗長化・配置は HardLinkedFile 単位とし、Namespace と Runtime の参照関係は Orchestration が管理する。
- HardLinkedFile は少なくとも1つの HardLink から参照されることを基本不変条件とする。ただし、最後の参照削除とオープン中ファイルの寿命・実体解放の詳細は別途設計する。

## 6. 関数と共通処理

- 公開関数は PascalCase、非公開関数は camelCase とする。
- 特定の操作でしか使用しない補助関数は、その操作のファイルに配置する。
- 複数の操作で使用する関数は、機能または責務に対応したファイルに配置する。
- `utils.go`、`helpers.go`、`common.go` のような、責務が曖昧な汎用ファイルを安易に作らない。
- 共通化する場合も、利用箇所と責務が明確になる単位で整理する。

## 7. ログ出力

- ログには Go 標準ライブラリの `log/slog` を使用する。
- 公開関数の実行結果（開始・完了、処理件数、所要時間、結果の要約など）は DEBUG レベルで記録する。通常処理のログを INFO 以上にしない。
- DEBUG ログには処理の入力を識別・把握するために必要なメタデータと、結果の要約を含める。データ本体、秘密情報、不要に大きな値は記録しない。
- WARNING / ERROR の出力要否とレベルは、各処理の意味・影響に基づいて個別に判断する。戻り値が error であることだけを理由に WARNING / ERROR を出力しない。
- 想定内のエラーや呼び出し側で通常処理として扱えるエラー（例: ファイルの存在・不存在、キャンセル、入力不正など）は、原則として WARNING / ERROR にしない。必要な場合も、通常の実行結果として DEBUG に記録する。
- WARNING は、処理を継続できるものの、利用者や運用者が把握すべき異常・例外的状況に限る。
- ERROR は、データ整合性・ファイルシステムの可用性などに実害を及ぼす、または処理の継続が困難な重大な問題に限る。
- 下位レイヤーで記録した問題を上位レイヤーで重複して記録しない。どのレイヤーがログを出すかは、その問題を適切に判断できるレイヤーに寄せる。
- 共通化はログ形式や共通属性の付与など、機械的に共通化できる範囲にとどめる。WARNING / ERROR の判定やメッセージを一律に決める共通処理は設けない。
- ログレベルは実行時の設定で変更できるようにする。DEBUG を無効にした場合、通常の実行ログを出力しない。

## 8. レイヤーの責務

各レイヤーの詳細な仕様は README.md を参照する。実装では、少なくとも次の責務分担を維持する。

### I/O 層

- 物理ファイルに対する低レベルの操作を担当する。
- データ配置方式や冗長化ポリシーを判断しない。
- 物理ファイルの列挙では、ファイルの識別情報と物理ファイルデータを返す。Header やチャンクの意味を解析しない。
- `ScanHeaders` は従来の `Scan` と同じく、StripeID が 0 の物理ファイルを列挙する。名前に Headers とあるが、Header 構造体への解析は行わず、物理ファイルデータを返す。
- `ListChunks` は StripeID による絞り込みをせず、全ストライプの物理ファイルを列挙する。こちらも物理ファイルデータを返し、チャンク情報の解釈は行わない。
- 起動時に全ストライプの情報を構築する場合は `ListChunks` を使用する。`ScanHeaders` は既存の StripeID 0 の列挙用途として維持する。全件列挙後にストライプ単位で再スキャンする API は設けない。

### Distribution 層

- ミラーリング、ストライピング、パリティ、データ復旧、配置移行を担当する。
- Header の形式や内容を知らず、Header を含む論理データを通常のバイト列として扱う。
- チャンク・ストライプ・バージョン・物理配置の情報を管理する。
- 起動時に I/O 層の `ListChunks` を利用して、全ストライプのチャンクおよびバージョン情報をメモリ上に構築する。永続インデックスは持たず、実ファイルから再構築する。
- 実際のチャンク単位の物理配置と、その配置情報を管理する。ただし、どのドライブ群を利用し、容量や書き込みサイズを踏まえてどう分散するかという配置方針は決定しない。
- Orchestration 層から渡された配置方針に従って、チャンクの物理配置を実現する。

### Orchestration 層

- 論理ファイルシステム全体の状態管理を担当する。
- 論理ファイルの名前空間、ディレクトリ階層、Object 間の親子関係、および各 Object の論理的な状態を管理する。
- 永続化された Object の Header から ParentID / Name に基づいてメモリ上の Directory children を構築し、HardLink の TargetID を解決して同一の Runtime ファイル実体を共有させる。
- Distribution 層から論理データを読み出し、その先頭に含まれる Header を解析して、ObjectID・ParentID・論理サイズ・ファイル属性などの論理ファイル情報を管理する。
- Header の定義、生成、解析、シリアライズ、および Header に保持する論理ファイル属性の更新・永続化を担当する。Header / Layout の内部表現、Kind 定数、Header 固有のエラーやサイズ定数は Orchestration パッケージ内に閉じ込め、外部へ公開しない。
- 起動時には Distribution 層が列挙した ObjectID をもとに、共通のストライプ読み出し API で論理データを取得し、Header を解析して論理ファイルシステムの状態を再構築する。
- Header を含む論理データは通常のストライプ読み書き API で扱う。Header 専用のストライプ処理経路は設けない。
- 論理ファイルに対する操作を受け付け、Header とファイルデータの整合性を保ちながら Distribution 層を利用する。
- ファイルごとの配置ポリシーを選択し、各ドライブの容量・状態や書き込みサイズなどを踏まえて、どのドライブ群にどう分散するかという配置方針を決定する。
- Distribution 層に配置方針を渡し、各層の処理を調整する。
- 容量管理やストレージ障害に対するシステム全体の対応を扱う。

### FUSE 層

- FUSE を通じたファイルシステムインターフェースを担当する。
- OS からの要求を Orchestration 層への要求に変換し、結果を返す。
- 配置方式や物理ストレージの内部実装を持たない。

## 9. 現在の設計議論と関数一覧

この節は、現在までに合意した責務分担と、議論に出ている関数一覧を記録する。ここに記載した関数名は、明示的に「確定」としたものを除き、実装 API の確定を意味しない。未決事項を実装都合で確定しない。

### 9.1 確定している設計

- Namespace は各 Object の Header にある ParentID と Name から復元する。Directory の children は永続化せず、起動時に Orchestration のメモリ上で構築する。
- root は ParentID が nil の唯一の Directory Object として識別する。HardLinkedFile も ParentID が nil だが、Kind によって root と区別する。
- Object Kind は File / Directory / Symlink / HardLinkedFile / HardLink を区別する。HardLinkedFile は Namespace 外で実データを保持し、HardLink は ParentID と TargetID を持つ Namespace Object で実データを保持しない。
- HardLink の TargetID は HardLinkedFile を直接指す。HardLink の連鎖、Directory / Symlink への参照、Directory のハードリンクは認めない。
- Symlink のリンク先パスは Header ではなく通常の論理データとして保持する。
- 永続化時は HardLinkedFile と各 HardLink を個別 Object として保存し、メモリ展開時は複数の HardLink Node が同一の Runtime ファイル実体を共有する。Distribution 上のデータ冗長化・配置単位は HardLinkedFile とする。

- チャンク、ストライプ、StripeVersion、物理チャンク配置の情報は Distribution 層の内部に閉じ込める。Orchestration 層にチャンク単位の内部情報を公開しない。
- `ChunkNo`、チャンク番号のビットフラグ、`DistributionMethod`、`StripeKeyInfo` などのチャンク表現・復元処理用型は Distribution パッケージ内だけで使用し、公開しない。チャンク番号の生成・判定関数や復元処理だけで使うエラー識別子も同様に非公開とする。
- Distribution の公開 API は論理ストライプ単位の操作と ObjectID の列挙を基本とし、チャンク番号・StripeVersion・物理配置を表す内部型を Request / Response に露出させない。配置方針を受け渡す型が必要な場合は、チャンク内部表現とは分離した API 契約として定義する。
- StripeVersion は Object 全体ではなく、ObjectID + StripeID ごとに管理する。
- 永続インデックスは作らない。起動時に実ファイルを全件列挙し、Distribution 層がストライプごとのチャンク・バージョン・物理配置情報をメモリ上に構築する。
- 起動時の全件列挙後は、通常のストライプ操作のために I/O 層へストライプ単位の再スキャンを要求しない。ストライプ情報は Distribution 層のメモリ上の情報を使う。
- I/O 層の既存 `Scan` は StripeID 0 の物理ファイル列挙として維持し、公開名を `ScanHeaders` とする。返すのは解析済み Header 構造体ではなく、従来どおり物理ファイルデータ（および物理ファイル識別情報）である。Header の解析・解釈は Orchestration 層が行う。
- I/O 層に `ListChunks` を追加する。これは StripeID で絞り込まず、全ストライプの物理ファイルを列挙し、物理ファイルデータ（および物理ファイル識別情報）を返す。チャンクの意味や有効性は I/O 層で解析しない。
- `ScanHeaders` と `ListChunks` は異なる用途の API であり、`ListChunks` の代わりに `ScanHeaders` のフィルターを外すだけ、という変更ではない。
- Orchestration 層が配置方針を決定する。各ドライブの容量・状態、書き込みサイズ、配置ポリシーなどを考慮して、どのドライブにどう分散するかを決める。
- Distribution 層は Orchestration 層から渡された配置方針を実現し、チャンク単位の実際の物理配置を管理する。Distribution 層がシステム全体の配置ポリシーを独自に決定してはならない。
- I/O 層は指定された Storage 上の物理ファイル操作と Storage に関する低レベル情報の提供を担う。配置方針は判断しない。
- ストライプ単位の書き込みは、対象ストライプの全チャンクの書き込みが完了した時点で上位層へ成功を返す。部分的なストライプ書き込みを成功として公開しない。
- 読み込みは、要求されたデータを返せる状態になった時点で応答する。通常のやり取りの基本単位はストライプとする。
- 有効バージョンの探索とバージョン更新は Distribution 層が担当する。有効性の判定は既存設計どおり grep に基づく。I/O 層は有効性を判断しない。
- チャンクごとのエラー情報を Distribution 層で記録することは確定している。エラー情報の具体的な形式・保持方法、および記録した情報をどの層・処理でどのように利用するかは未決事項とする。
- ストライプの整合性確認と復旧は Distribution 内部の管理処理とし、`CheckStripe` / `RecoverStripe` のような公開関数として外部に提供しない。内部関数 `checkStripe` / `recoverStripe` などから必要に応じて実行する。外部から個別に管理・指示させない。
- パリティ方式は XOR、NumParities=1 を前提とする。復元可能なのはデータチャンク1個の欠損まで。複数欠損や整合性不一致はエラーとする。
- `restoreStripe` は方式別復元処理へのディスパッチを担い、Mirror / Parity それぞれの復元処理が方式固有の検証を行う。旧 StripeVersion へのフォールバックは方式別復元関数に混ぜず、読み込み・復元フロー側で扱う。

### 9.2 I/O 層の関数一覧

| 関数 | 位置付け | 内容 |
|---|---|---|
| `New` | 既存 API | I/O インスタンスを生成する |
| `Read` | 既存 API | 指定 Storage の物理ファイルを読み込む |
| `Write` | 既存 API | 指定 Storage に物理ファイルを書き込む。既存ファイルの上書き可否などは既存契約に従う |
| `Delete` | 既存 API | 指定 Storage の物理ファイルを削除する |
| `ScanHeaders` | 既存 Scan の改名案 | StripeID 0 の物理ファイルを列挙し、物理ファイルデータを返す。Header 構造体には解析しない |
| `ListChunks` | 追加する API 案 | 全 StripeID の物理ファイルを列挙し、物理ファイルデータを返す。Distribution の起動時全展開に使う |
| Storage 情報取得 | API 未確定 | Orchestration が配置方針を決めるために必要な Storage 一覧・容量・状態などを提供する手段。関数名・取得範囲は未確定 |

I/O の列挙結果には、Distribution が物理ファイルを識別して配置情報を構築できる情報が必要である。ただし、I/O 層は FileKey 等の物理識別情報を超えて Header やチャンクの意味を解析しない。

### 9.3 Distribution 層の関数一覧

#### 公開 API 候補

| 関数 | 位置付け | 内容 |
|---|---|---|
| `New` | 候補 | I/O の `ListChunks` を使って全物理ファイルを同期的に解析し、ストライプ・チャンク・バージョン・配置情報をメモリに構築した Distribution インスタンスを返す。初期化に失敗した場合は error を返す |
| `ListObjects` | 公開 API 候補（旧 `EnumerateHeaders` の置き換え） | Distribution が把握している ObjectID を列挙する。Header の読み出し・解析は行わない |
| `ReadStripe` | 候補 | 指定ストライプの論理データを読み込み、必要に応じて復元する。Header を含むかどうかで処理を分けない |
| `WriteStripe` | 候補 | Header を含むかどうかに関係なく、指定ストライプの論理データを配置方針に従って書き込む。全チャンクの書き込み完了後に成功を返す |
| `DeleteStripe` | 候補・要確認 | 指定ストライプの削除。削除の単位や Header との関係は要確認 |

Distribution の公開 API はストライプ単位を基本とする。Header 専用の読み書き API は設けず、Header を含むストライプも通常のストライプとして扱う。Orchestration はチャンク情報や StripeVersion の内部表現を直接操作しない。

#### 内部処理候補

| 関数 | 内容 |
|---|---|
| `findValidVersions` | ストライプの有効バージョンを探索する |
| `updateVersionInfo` | メモリ上のバージョン情報を更新する |
| `splitStripe` | ストライプデータをチャンクに分割する |
| `combineStripe` | チャンクをストライプデータに結合する |
| `generateParity` | パリティを生成する |
| `restoreStripe` | 方式別復元処理へのディスパッチ |
| `restoreMirror` | Mirror 方式の復元 |
| `restoreParity` | Parity 方式の復元 |
| `checkStripe` | チャンク間の整合性を確認する |
| `recoverStripe` | ストライプの復旧を実行する |
| `writeChunks` | 配置方針に従ってチャンクを物理書き込みする |
| `deleteVersion` | 指定バージョンの物理ファイルを削除する |

上記の内部関数名・分割は候補であり、実装時に責務が重複する場合は整理してよい。ただし、方式別復元の検証責務やストライプ単位の原子性を崩さない。

### 9.4 Orchestration 層の関数一覧

#### 公開 API 候補

| 関数 | 位置付け | 内容 |
|---|---|---|
| `New` | 候補 | Distribution の `ListObjects` で ObjectID を列挙し、共通の `ReadStripe` で論理データを読み出して Header を解析し、Orchestration の State を同期的に構築したインスタンスを返す。初期化に失敗した場合は error を返す |
| `Open` | 既存設計の操作候補 | Object を開く |
| `Read` | 既存設計の操作候補 | Object の指定範囲を読み込む |
| `Write` | 既存設計の操作候補 | Object の指定範囲を書き込む |
| `Truncate` | 既存設計の操作候補 | Object のサイズを変更する |
| `Fsync` | 既存設計の操作候補 | Object の変更を永続化する |
| `Allocate` | 既存設計の操作候補 | Object の領域を確保する |
| `Lseek` | 既存設計の操作候補 | Object 内のデータ領域を探索する |
| `Release` | 候補 | Object のハンドルを解放する |
| `GetAttr` | 候補・責務要確認 | 属性を取得する |
| `SetAttr` | 候補・責務要確認 | 属性を変更する |
| `Create` | 候補・責務要確認 | Object を作成する |
| `Mkdir` | 候補 | Directory を作成する |
| `Rmdir` | 候補 | 空の Directory を削除する |
| `Rename` | 候補 | Namespace 上の Object 名・親を変更する |
| `Link` | 候補 | 既存ファイル実体への HardLink を作成する |
| `Symlink` | 候補 | Symlink Object とリンク先パスデータを作成する |
| `Readlink` | 候補 | Symlink のリンク先パスデータを取得する |
| `Unlink` | 候補・責務要確認 | Object を削除する |

上記の公開 API 一覧は議論時点の候補であり、すべてが確定したわけではない。特に namespace 操作・属性操作の最終的な責務境界は README.md および今後の設計合意に従う。

#### 内部処理候補

| 関数 | 内容 |
|---|---|
| `selectStorages` | 配置方針に使用する Storage を選択する |
| `planPlacement` | データサイズ・容量・状態・配置ポリシーから配置計画を作成する |
| `getStorageCapacity` | Storage の容量情報を取得する |
| `splitWriteRange` | Object の書き込み範囲をストライプ単位に分割する |
| `readStripes` | 必要なストライプを読み込む |
| `writeStripes` | 必要なストライプを書き込む |
| `updateState` | Distribution での永続化成功後に Orchestration の State を更新する |

配置計画を Distribution に渡す具体的な Request 型・フィールド・チャンク枠との対応関係は未確定。Orchestration がチャンク内部情報を持つ設計にはしない。Distribution は配置計画を実際のチャンク配置へ対応付ける。

### 9.5 FUSE 層

FUSE 層の具体的な関数一覧は、この議論ではまだ整理していない。OS/FUSE の操作を Orchestration の公開 API に変換する責務のみを確定事項とする。

### 9.6 未決事項・今後決めること

- Storage 情報（一覧、容量、状態）を I/O 層から Orchestration 層へ提供する具体的な API とデータ型。
- Orchestration が作成する配置計画の具体的な形式、および Distribution のストライプ構成・チャンク枠との対応付け。
- Distribution の `DeleteStripe` の必要性・責務・呼び出し元。
- Header の作成・更新・削除と、ストライプ書き込みの確定順序。
- namespace 操作・属性操作（`Create`、`Unlink`、`GetAttr`、`SetAttr` 等）の最終的なレイヤー境界。
- HardLinkedFile / HardLink の属性情報の保持場所と FUSE inode / st_nlink への対応。
- 最後の HardLink 削除時の HardLinkedFile の解放条件、オープン中ファイルの寿命、および参照数の再構築方法。
- ハードリンク作成・削除など複数 Object の永続化を伴う Namespace 操作の原子性とクラッシュ復旧手順。
- root の一意性検証、および Namespace から到達できない Object を起動時にどう扱うか。
- ストレージ障害・容量変化を踏まえた配置計画の再評価と、既存チャンクの再配置方針。
- Distribution が記録するチャンクごとのエラー情報の具体的なデータ形式・保持方法、および記録情報の利用方法・利用主体。
- ストライプ書き込みの原子性をクラッシュ時にも保証する具体的な確定・復旧手順。

## 9. テスト

- 実装した操作・機能には、対応するテストを用意する。
- レイヤーごとに独立してテストできる構成を維持する。
- 内部実装の詳細だけでなく、公開 API の契約と外部から観測できる振る舞いを検証する。
- 振る舞いや契約を変更した場合は、関連するテストも更新する。

## 10. Pull Request の運用

- 作業を開始する前、および新しい Pull Request を作成する前に、リポジトリに Open 状態の Pull Request が残っていないか確認する。
- Open 状態の Pull Request があり、今回の変更がその作業に関連する場合は、新しい Pull Request を作成せず、既存 Pull Request のブランチに追加コミットする。
- 既存 Pull Request と今回の変更が無関係で、独立した作業として扱うべき場合に限り、新しい Pull Request を作成する。
- 既存 Pull Request が複数ある場合は、変更内容に対応する Pull Request を選ぶ。判断できない場合は、作業を進める前に確認する。
- Pull Request を作成した後も、その Pull Request が Open の間は、同じ作業に関する修正・追加を同じブランチに積む。
