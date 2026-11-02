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
- Header は論理削除状態を示す `Deleted bool` を持ち、バイナリでは 0 / 1 の 1 byte として保存する。1 以外は不正 Header とする。起動時に Orchestration は `Deleted == true` の ObjectID をメモリ上の削除キューへ登録する。Header で追加する参照情報は nullable な TargetID のみとし、TargetID は HardLink の場合だけ設定する。HardLinkedFile を識別する追加フラグは設けない。TargetID が存在する場合は UUIDv4 として検証し、nil はゼロ UUID ではなく明示的な存在情報で表す。
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
- 有効バージョンの探索とバージョン更新は Distribution 層が担当する。有効バージョンは Distribution のインメモリ管理情報に保持された最新 Version を正とする。I/O 層は有効性を判断しない。
- Distribution は要求された操作を実行し、成功時は結果、失敗時はエラーを返す。内部状態として管理するのは、Stripe ごとの Version 情報と Object の存在情報を基本とする。物理データの途中状態を有効状態として公開せず、処理途中の失敗で既存の有効状態を壊さない。
- IO ごと・全体のエラーカウントおよび Stripe 単位の破損状態を Distribution 層の非公開フィールドで保持する。外部からの参照には公開 getter を使用する。エラーカウント等は揮発性とし、定期的にログ出力する。外部コマンド向けに取得 API を提供し、長期保存が必要な場合は外部ログ解析ツールを利用する。
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

#### 公開 API

| 関数 | 位置付け | 内容 |
|---|---|---|
| `New` | 公開 API | `NewRequest.IOs []*io.IO` を受け取り、スライスの index を IO の識別子として使用する。全 IO の `ListChunks` を走査し、読み込めた Stripe 情報と障害状態を Distribution インスタンスの非公開状態フィールドに構築する。外部からの参照には公開 getter を使用する。戻り値は初期化成功またはクリティカルなエラーのみとし、IO 単位・Stripe 単位の部分障害は戻り値ではなく、公開 getter を通じて参照できるようにする。利用可能な IO が 1 つもない場合はクリティカルな初期化エラーとする。 |
| `ListObjects` | 公開 API | Distribution が把握している ObjectID を列挙する。Header の読み出し・解析は行わない |
| `ReadStripe` | 公開 API | 指定 Stripe の論理データを読み込み、必要に応じて復元して返す。復元可能な欠損・破損は成功として扱い、WARNING ログと IO ごとのエラーカウントを記録する。復元不能な場合は上位層にデータ破損を表すエラーコードを返し、ERROR ログ、IO ごと・全体のエラーカウント、および Stripe 単位の破損状態を記録する |
| `WriteStripe` | 公開 API | 配置方式（Mirror / XOR Parity とチャンク数）、IO index 配列、論理ストライプデータを受け取る。Chunk の種類・番号・分割・配置は Distribution が決定する。全 Chunk の書き込み成功後にのみ新 Version をインメモリ上で有効化する。IO index は重複せず有効範囲内であることを検証する |
| `DeleteObject` | 公開 API | Orchestration から指定された Object の物理データを削除する。Header 以外の Stripe / Version を先に削除し、最後に Header を削除する |
| `Cleanup` | 公開 API | 古い Version、無効な Stripe、不完全チャンクなど、Object 全体の論理削除とは別の不要データを整理する。削除対象の種類を bool フィールドで個別に指定する |
| `GetErrorStats` | 公開 API | IO ごと・全体のエラーカウントなど、揮発性の診断状態を外部コマンドから取得する |

Distribution の公開 API は論理ストライプ単位の読み書き操作、ObjectID の列挙、および Object 単位の物理削除を提供する。Header 専用の読み書き API や `DeleteStripe` は設けない。Object の論理削除状態は Orchestration が管理し、物理削除は Orchestration からの明示的な `DeleteObject` 呼び出しで実行する。Distribution の `Cleanup` は Orchestration の `Cleanup` から呼び出され、論理削除済み Object の判定や ObjectID の選定は行わない。Distribution は Header の内容を解釈しない。

#### 公開 API の共通契約・確定事項

- 実行系 Request には `context.Context` を含める。
- `NewRequest` は `IOs []*io.IO` を受け取る。IO の識別子は渡されたスライスの index とし、IO 自体に識別子を追加することは前提としない。
- 同一の `*io.IO` インスタンスを複数回登録することは禁止する。物理ドライブや実体パスが同一かどうかまでは判定しない。`New` ではポインタの重複を検証し、IO index のリストを受け取る要求では index の重複・範囲を検証する。
- Orchestration は利用する IO の index の集合だけを指定する。Chunk の種類・番号・パリティ・IO 内での配置は Distribution が決定する。
- IO index の重複、範囲外指定、必要数を満たさない指定はエラーとする。
- `New` は全 IO を走査し、正常に読み込めた Stripe 情報を構築する。IO へのアクセス段階で発生したエラーと、Stripe を認識した後にその Stripe のデータを読み込めなかったエラーを区別して Distribution インスタンスの非公開状態フィールドに保持し、公開 getter を通じて参照できるようにする。戻り値は初期化成功またはクリティカルなエラーのみとする。利用可能な IO が 1 つもない場合はクリティカルな初期化エラーとする。部分障害がシステム全体にとってクリティカルかどうかは Orchestration が公開状態を参照して判断する。
- `ReadStripe` で指定 Stripe が存在しない場合はエラーを返す。復元可能な欠損・破損は復元して成功を返す。復元不能時は上位層に詳細な物理障害情報を露出せず、データ破損を表すエラーコードを返す。
- `ReadStripe` で破損を検出した場合、修復に使用できる IO index の候補配列を受け取り、必要最低数以上の候補があることを検証したうえで、配列の先頭から順に書き込みを試みる。必要最低数は Mirror 方式では 1、XOR パリティ方式ではデータチャンク数とする。現時点では XOR 以外のパリティ方式は存在しない前提とする。候補の切り替え・再配置方針の判断は Orchestration が行い、Distribution は渡された順序に従って試行する。
- エラー情報は IO ごとのカウントと全体カウントを持ち、Stripe 単位で破損状態を Distribution の非公開状態として保持する。復元可能な異常は WARNING、復元不能なデータ破損は ERROR として記録する。エラーカウント等の診断状態はメモリ上のみで保持し、定期的にログ出力する。永続化は行わず、外部コマンド向けの取得 API `GetErrorStats` を提供する。getter は内部の map / slice 等を直接変更できない値として返す。必要な長期保存は外部ログ解析ツールに委ねる。
- `WriteStripeRequest` は配置方式の指定、書き込み先 IO index の配列、論理ストライプのデータ本体を受け取る。配置方式の指定には Mirror / XOR Parity の方式と必要なチャンク数（Mirror の複製数、Parity のデータチャンク数）を含める。Distribution はこの指定と IO index 配列に従ってチャンクを生成・配置する。
- `WriteStripe` は新 Version の全 Chunk の物理書き込みが完了した場合にのみ、インメモリの最新 Version を更新する。途中失敗時は新 Version を有効化せず、既に物理的に書き込まれた不完全データは破損データとして残す。失敗を ERROR ログに記録し、呼び出し元へエラーを返す。
- Distribution は `WriteStripe` 成功直後に旧 Version を自動削除しない。不要 Version の削除は `Cleanup` が担う。
- Distribution 内部では同一トランザクションの処理を並列実行しない。トランザクション間の排他・調停は Orchestration の責務とし、Distribution 内部のロック機構は現時点では実装しない。
- Object の論理削除状態と削除対象の管理は Orchestration が担当する。Distribution 側に削除済み状態を重複して保持したり、物理ファイル名などに別の削除マーカーを設けたりしない。起動時に Orchestration が Header を読み、`Deleted == true` の ObjectID をメモリ上の削除キューへ登録する。削除キューは Header から再構築し、別の永続マーカーは設けない。
- Orchestration の `Cleanup` は、Header の `Deleted` フラグから起動時に再構築したメモリ上の削除キューを処理し、対象ごとに Distribution の `DeleteObject` を呼び出す。その後、Distribution の `Cleanup` を呼び出す。
- Distribution は Header の内容・論理削除状態を解釈せず、`DeleteObject` で指定された Object の物理データを削除する。削除対象の選定や論理削除状態の判定は行わない。
- `DeleteObject` は Header 以外の Stripe / Version を先に削除し、すべての非 Header データの削除が完了した場合に限り、最後に Header を削除する。途中で削除に失敗した場合は Header を残し、次回の Orchestration `Cleanup` で再試行できるようにする。Header 自身の削除に失敗した場合も同様に Header を残す。
- 物理削除の個別失敗は論理削除の取り消しを意味しない。削除対象であることを Orchestration のメモリ上の状態に保持し、次回の Orchestration `Cleanup` で `DeleteObject` を再実行する。物理削除の成否と論理削除の成否を分離する。
- Distribution の `Cleanup` は Object 全体の論理削除を実行せず、古い Version、無効な Stripe、WriteStripe 失敗時に残った不完全チャンクなどを整理する。これらの削除対象の種類は bool フィールドで個別に on/off 指定する。無効な Stripe はサルベージ目的で保持する場合があるため、常に削除する前提にしない。Version の有効性は Distribution のインメモリ管理情報にある最新 Version を正とする。
- `Cleanup` は通常操作のフォールバック対象のみを削除する。トランザクション間の並行実行制御は Orchestration が担い、Distribution 内部ではロックを実装しない。
- エラーの分類・判定は一箇所に集約し、その判定結果をログに記録する。ログ出力のために別のエラー判定を重複させない。上位層で同じ問題を重複してログ出力しない。
- 処理途中で失敗した場合はその場で中断し、有効なインメモリ状態や既存の有効データを壊さない構造にする。物理的に残る不完全データは有効状態として扱わず、後続の Cleanup 対象にできるようにする。

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
| `Unlink` | 候補・責務要確認 | Namespace 上の名前を削除し、必要に応じて実体の論理削除状態を更新する |
| `Cleanup` | 公開 API 候補 | メモリ上の状態から論理削除済み Object を特定し、各 Object に `Distribution.DeleteObject` を実行した後、`Distribution.Cleanup` を呼び出す |

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
- namespace 操作・属性操作（`Create`、`Unlink`、`GetAttr`、`SetAttr` 等）の最終的なレイヤー境界。
- HardLinkedFile / HardLink の属性情報の保持場所と FUSE inode / st_nlink への対応。
- 最後の HardLink 削除時の HardLinkedFile の解放条件、オープン中ファイルの寿命、および参照数の再構築方法。
- ハードリンク作成・削除など複数 Object の永続化を伴う Namespace 操作の原子性とクラッシュ復旧手順。
- root の一意性検証、および Namespace から到達できない Object を起動時にどう扱うか。
- ストレージ障害・容量変化を踏まえた配置計画の再評価と、既存チャンクの再配置方針。
- `New` 時の利用可能 IO の判定基準（IO へのアクセスが成功したとみなす条件）と、非公開状態フィールドおよび getter の具体的な名前・戻り値の構造。
- `GetErrorStats` の Request / Response の具体的なフィールド構成と、定期ログ出力の間隔・出力内容。getter が返す状態スナップショットの具体的な型・構造。
- 上位層向けエラーコードの一覧と、内部診断情報との分離方法。
- ストライプ書き込みの原子性をプロセス停止・電源断などのクラッシュ時にも保証する具体的な確定・復旧手順。

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
