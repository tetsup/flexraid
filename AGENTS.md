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
- `ParentID` は nullable とする。通常の Namespace Object は有効な UUIDv4 の親 ID を持つが、root Directory と Namespace に直接所属しない HardLinkedFile は `nil` を持つ。HardLinkedFile は Namespace に直接所属する場合も、HardLink 経由でのみ参照される場合もある。
- root 自身も通常の Object として有効な ObjectID を持つ。root は ParentID が nil の唯一の Directory Object として識別し、root の子の ParentID には root 自身の ObjectID を設定する。
- HardLinkedFile は ParentID が nil になる場合があるため、ParentID の nil だけで root と判定してはならない。必ず Kind と組み合わせて判定する。
- nullable な識別子を永続化するときは、ゼロ UUID を null の代用にせず、存在フラグなどで有無を明示する。存在する場合の ID は必ず UUIDv4 として検証する。

### ObjectKind、Namespace、HardLink

- 論理 Object の Kind は Orchestration が所有する。Kind は File、Directory、Symlink、HardLinkedFile、HardLink を区別する。
- File は ParentID を持ち、実データを保持する。
- Directory は root 以外 ParentID を持つ。Directory の children は永続化せず、起動時に各 Object の ParentID と Name から Orchestration のメモリ上に構築する。
- Symlink は ParentID を持つ。リンク先パス文字列は Header ではなく通常の論理データとして保持する。
- HardLinkedFile は実データを保持する実体であり、ParentID は nullable、TargetID は持たない。ParentID がある場合は通常の名前付き Namespace Object として Directory の children に登録し、nil の場合は Namespace に直接所属せず HardLink 経由でのみ参照される。
- HardLink は Namespace 上の名前を表す Object であり、ParentID と TargetID を持つ。TargetID は必ず HardLinkedFile の ObjectID を直接指し、HardLink は実データを保持しない。
- Header は論理削除状態を示す `Deleted bool` を持ち、バイナリでは 0 / 1 の 1 byte として保存する。1 以外は不正 Header とする。起動時に Orchestration は `Deleted == true` の ObjectID をメモリ上の削除キューへ登録する。Header で追加する参照情報は nullable な TargetID のみとし、TargetID は HardLink の場合だけ設定する。HardLinkedFile を識別する追加フラグは設けない。TargetID が存在する場合は UUIDv4 として検証し、nil はゼロ UUID ではなく明示的な存在情報で表す。
- リリースまでは、Header のバイナリ構造を変更しても `FormatVersion` は `0` のままとし、後方互換性の維持や旧形式の読み込み対応は行わない。`HeaderSize` は現在の構造に合わせて更新する。
- HardLink の連鎖、Directory / Symlink を参照先とする HardLink、Directory 自体のハードリンクは認めない。HardLink は Directory ではなく、children を持つ親にはなれない。
- ハードリンク作成時は、元の File Object を同じ ObjectID の HardLinkedFile に変換し、元の ParentID / Name を維持する。新しい名前は HardLink Object として追加し、その TargetID は HardLinkedFile を直接指す。これにより元の名前と追加された名前は対等に同じ実体を参照する。既に HardLinkedFile である元ファイルへの追加 Link は、同じ実体を指す HardLink を追加する。HardLinkedFile の ParentID は、元の名前が削除された後に nil となり得る。
- 永続化時は実体と各 Namespace Object を個別の Object として保存する。メモリ展開時は Directory の children を構築し、各 HardLink Node が同じ Runtime ファイル実体を共有するように解決する。
- データ実体の Distribution 上の冗長化・配置は HardLinkedFile 単位とし、Namespace と Runtime の参照関係は Orchestration が管理する。
- HardLinkedFile は、ParentID による Namespace への直接所属、または到達可能な HardLink からの参照の少なくとも一方を持つことを基本不変条件とする。ParentID が nil であることだけを理由に孤立・削除可能と判定してはならない。
- `UnlinkHardLinkedFile` は ParentID が nil の場合エラーを返す。ParentID がある場合は Header の ParentID を nil にするだけで、Deleted や削除キューを操作せず、実体を保持する。
- `UnlinkHardLink` は対象 HardLink の名前を外した後、参照先 HardLinkedFile の残りの Namespace 参照を確認して更新する。HardLink が残る場合は実体を維持し、HardLink がなく ParentID がある場合は実体を通常の File に戻す。ParentID も HardLink 参照もない場合は Deleted=true として Cleanup 対象にする。
- HardLink と参照先 Header の更新はシリアルに永続化し、複数 Header 更新をまたぐ Atomic 性は要求しない。中断時に一時的な不整合が残り得ることを前提とする。

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

- Orchestration から受け取るのは論理ストライプ単位のデータであり、論理ファイルの offset、ファイルサイズ、ファイル内の位置関係を扱わない。
- `ReadStripe` / `WriteStripe` の StripeID は、Orchestration が論理ファイルの offset と Header の配置計画から決定する。Distribution は StripeID を識別子として扱い、論理 offset への変換を行わない。
- `WriteStripe` は受け取ったストライプデータを指定 ChunkSize と Layout に従ってチャンクへ分割し、必要なパリティを生成して、指定された IO index へ永続化する。論理ファイルの書き込み範囲の分割や既存ストライプデータとのマージは行わない。
- Orchestration が論理ファイル offset を StripeID とストライプ内 offset に変換する際は、Header 予約領域 1 KiB をファイル本体 offset から分離する。Stripe 0 の物理チャンクサイズは 4 KiB 固定とし、論理データチャンク数を N としたとき、Stripe 0 のファイル本体容量 S0 は `4 KiB × N - 1 KiB` とする。Mirror の N は 1（複製数ではない）、XOR Parity の N はデータチャンク数とする。
- Stripe 0 の先頭 1 KiB に Header を格納し、残りをファイル本体に使用する。Stripe 1 以降のチャンクサイズ C は Header に保持し、通常ストライプの論理容量は `C × N` とする。ファイル本体 offset x が S0 未満なら StripeID 0、StripeID 0 内のデータ offset は `1 KiB + x`。x が S0 以上なら StripeID は `1 + (x - S0) / (C × N)`、ストライプ内 offset は `(x - S0) % (C × N)` とする。
- Header の配置計画を変更するのは論理ファイルサイズが変わるときだけとする。サイズを変えない上書きでは再計画しない。Flush / Fsync は Header に確定済みの配置計画を使用し、配置を再計算・上書きしてはならない。サイズ変更で配置計画が変わる場合は、既存データを新しいストライプ境界・チャンクサイズへ移行してから Header とデータを一貫した状態にする。

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
- 起動時の初期化では、公開 `New` が Distribution の `ListObjects` で ObjectID を列挙し、共通の `ReadStripe` API で各 Object の Header を含む初期データを取得して、非公開 `initialize` に渡す。`initialize` は全 Header を解析・検証し、Runtime Object を生成して ParentID / Name から Directory children を構築し、HardLink の TargetID を解決する。その後、単一の非公開 `inspect` で構築結果全体を検査する。root が一意に定まらないなど Namespace 全体を復元できない不整合は初期化エラーとする。一方、壊れた HardLink や Namespace ツリー外の Object など、一部の機能に限定された不整合は該当要素を Namespace 上に存在しないものとして扱い、warning を出して初期化を続行する。この場合、該当する HardLink / Object および関連する実体を Cleanup の削除対象にしてはならない。部分的不整合を理由に永続データを自動削除・論理削除扱いへ変更してはならない。Distribution への I/O は `New` が担当し、`initialize` は受け取ったデータを使って構築・検査する。
- Header を含む論理データは通常のストライプ読み書き API で扱う。Header 専用のストライプ処理経路は設けない。
- 論理ファイルに対する操作を受け付け、Header とファイルデータの整合性を保ちながら Distribution 層を利用する。
- ファイルごとの配置ポリシーを選択し、各ドライブの容量・状態や書き込みサイズなどを踏まえて、どのドライブ群にどう分散するかという配置方針を決定する。
- 既にストライプが永続化されている場合は、Distribution が保持する各チャンクの配置情報から、データチャンク順・パリティ最後の IO index 配列を取得して再利用する。必要なチャンク数分だけを渡し、余分な IO index は渡さない。
- 新規ストライプなど配置が未確定の場合は、Distribution から IO ごとの利用可能容量を取得し、Orchestration が残容量の多い順に並べ、必要なチャンク数分だけを選択する。同容量の場合は IO index 順を維持し、決定的な結果にする。
- Distribution 層に配置方針を渡し、各層の処理を調整する。
- 容量管理やストレージ障害に対するシステム全体の対応を扱う。
- 論理ファイルシステムの判断に必要なストレージ情報（容量・状態など）は Orchestration に集約する。I/O 層から取得すべき物理情報は下位 API を通じて取得し、配置・容量判断に使いやすい形で Orchestration のメモリ上にキャッシュする。キャッシュは Storage の実状態そのものではなく判断用スナップショットであり、更新・再取得条件を管理する。
- FUSE \`Access\` は \`GetAttr\` の属性と要求元 credentials を使ってアクセス可否を判定する。Access 専用の Orchestration API は設けない。ただし \`GetAttr\` の成功だけをアクセス許可とみなしてはならない。
- FUSE \`StatFs\` は Orchestration が集約・キャッシュした容量情報を返す。\`GetAttr\` は Object 単体の属性 API であり、StatFs の代替ではない。容量情報の取得・集計・利用可能容量の意味づけは Orchestration の責務とし、FUSE は値を statfs 応答へ写像する。

### FUSE 層

- FUSE を通じたファイルシステムインターフェースを担当する。
- OS からの要求を Orchestration API に変換し、結果を返す。Namespace / metadata / file data の状態や判断を FUSE 内に複製しない。
- 配置方式や物理ストレージの内部実装を持たない。
- \`Access\` は \`GetAttr\` で得た mode / UID / GID 等と要求元 credentials（UID / GID / supplementary groups）および POSIX の root 規則で判定する。属性取得成功だけで Access 成功としない。
- \`StatFs\` は Orchestration が返す容量スナップショットを statfs fields に写像する。FUSE が I/O 層へ直接問い合わせたり、独自に容量を集計したりしない。
- \`Unlink\` と \`Rmdir\` は POSIX 上別操作とする。通常 File / Symlink / HardLink の名前削除は Orchestration \`Unlink\`、Directory 削除は空判定など Directory 固有契約を持つ \`Rmdir\` に対応づける。内部 helper の共有は可能だが、公開操作・種別検証・エラー意味は混同しない。
- \`Rename\` は POSIX rename（\`mv\` 相当）であり、名前変更だけでなく親 Directory 間の移動を含む。FUSE は source / destination を Orchestration に渡し、種別別状態遷移を実装しない。

## 9. 現在の設計議論と関数一覧

この節は、現在までに合意した責務分担と、議論に出ている関数一覧を記録する。ここに記載した関数名は、明示的に「確定」としたものを除き、実装 API の確定を意味しない。未決事項を実装都合で確定しない。

### 9.1 確定している設計

- Namespace は各 Object の Header にある ParentID と Name から復元する。Directory の children は永続化せず、起動時に Orchestration のメモリ上で構築する。
- root は ParentID が nil の唯一の Directory Object として識別する。HardLinkedFile も ParentID が nil だが、Kind によって root と区別する。
- Object Kind は File / Directory / Symlink / HardLinkedFile / HardLink を区別する。HardLinkedFile は ParentID があれば Namespace に直接所属し、nil なら HardLink 経由で参照される実データ実体とする。HardLink は ParentID と TargetID を持つ Namespace Object で実データを保持しない。
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
- パリティ方式は XOR、NumParities=1 を前提とする。復元可能なのはデータチャンク1個の欠損まで。
- ストライプ0と通常ストライプのサイズ・シーク仕様は次のとおりとする。
  - ストライプ0の先頭 1 KiB は Header の予約領域とする。Header はこの予約領域に収める。
  - ストライプ0の物理チャンクサイズは 4 KiB 固定とする。
  - ストライプ0の論理サイズは、4 KiB × データ分散数 N とする。N はパリティチャンクを除いた論理データチャンク数とし、Mirror では複製数ではなく論理データチャンク数 1 として数える。
  - ストライプ0に格納できるファイル本体のデータ容量は、ストライプ0の論理サイズから Header 予約領域 1 KiB を引いた値（4 KiB × N - 1 KiB）とする。予約領域以外はパディング専用にせず、ファイル本体の実データを格納する。
  - ストライプ1以降のチャンクサイズは、ストライプ0の Header 予約領域に記録されたチャンクサイズを使用し、同一 Object の通常ストライプでは固定とする。
  - 通常ストライプの論理データ容量を C × N（C は Header に記録された通常チャンクサイズ）とする。ストライプ0のデータ容量を S0 = 4 KiB × N - 1 KiB とすると、ストライプ i（i >= 1）の論理開始位置は x = S0 + (i - 1) × C × N、すなわち x = (4 KiB + C × (i - 1)) × N - 1 KiB とする。
  - 任意の論理オフセット x >= S0 から通常ストライプ番号を求める場合は i = 1 + floor((x - S0) / (C × N))、ストライプ内オフセットは (x - S0) mod (C × N) とする。ストライプ0の範囲は別扱いとする。
- RAID の復元トリガーは、チャンクの欠落または物理読み取りエラーによってチャンクを取得できないことに限定する。取得できたチャンク同士の内容比較や、データチャンクとパリティの整合チェックは復元判定に使用しない。
- Mirror で複製内容が異なる場合も、Parity でパリティがデータと一致しない場合も、それだけでは読み込みエラーにしない。取得可能なデータを返し、RAID 層でどの値が正しいかを推測・訂正しない。内容の変化・改ざん検出は RAID の保証範囲外とする。
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

#### 公開 API の実装状況と FUSE 対応

この表は現時点の Orchestration 公開 API を整理する。\`ReadLink\` の PascalCase を正式名とし、\`Readlink\` は使用しない。実装済みは API の存在を示し、POSIX / FUSE の全エッジケース実装・テスト完了を意味しない。

| Orchestration API | 状況 | FUSE 操作・責務 |
|---|---|---|
| \`New\` | 実装済み | Filesystem / mount 初期化 |
| \`Lookup\` / \`GetAttr\` / \`ReadDir\` | 実装済み | \`Lookup\` / \`Getattr\` / \`Readdir\`。GetAttr は handle 非依存 |
| \`Create\` / \`Mkdir\` / \`Symlink\` | 実装済み | 同名 callbacks。Create は FileHandle を返す |
| \`ReadLink\` | 実装済み | \`Readlink\`。リンク先文字列を返し、追跡しない |
| \`Link\` / \`Unlink\` | 実装済み | HardLink 作成、通常 File / Symlink / HardLink 名の削除。Unlink は Directory 対象外 |
| \`Open\` / \`Read\` / \`Write\` | 実装済み | handle-backed file operations |
| \`Flush\` / \`Fsync\` / \`Release\` | 実装済み | 同名 callbacks。lifecycle semantics は Section 11 |
| \`Truncate\` / \`SetAttr\` | 実装済み | \`Setattr\`。サイズ変更は data I/O と同期 |
| \`Cleanup\` | 実装済み | FUSE callback ではなく Orchestration 管理処理 |
| \`Rmdir\` | 未実装 | 空 Directory の検証・削除を行う独立操作 |
| \`Rename\` | 未実装 | Namespace entry の改名・親移動・destination 置換 |
| \`Allocate\` / \`Lseek\` | 未実装 | \`Fallocate\` / \`Lseek\`。それぞれ空間確保、\`SEEK_DATA\` / \`SEEK_HOLE\` |
| StatFs 用容量 API | 要追加 | \`Statfs\`。Orchestration の容量スナップショットを返す |
| Access 専用 API | 不要 | \`Access\`。GetAttr 属性と request credentials で FUSE 側判定 |
| Directory handle API | 未整理 | regular-file FileHandle と分離 |
| advisory lock API | 未対応 | \`Getlk\` / \`Setlk\` / \`Setlkw\`。実装・テスト前に mount option を有効化しない |
| \`Mknod\` / \`Tmpfile\` | 方針未確定 | callback を有効化する前に Object kind / handle 契約を定義 |

\`Access\` は属性取得 API とは別の判定だが、Orchestration の新規公開 API は不要とする。FUSE は GetAttr の mode / UID / GID 等と request credentials を用いる。\`StatFs\` は filesystem 全体の容量情報を必要とするため、Orchestration に容量情報取得 API を追加する。

\`Unlink\` は通常 File / Symlink / HardLink の Namespace name removal を扱い、Directory は対象外とする。\`Rmdir\` は独立公開操作として空 Directory の条件、親 Directory NLink の派生、Directory 固有のエラーを扱う。内部 helper の共有は可能だが、公開 API / POSIX semantics を Unlink に吸収しない。

\`Rename\` は POSIX rename / \`mv\` 相当であり、同じ親内の改名に限らない。source の Name を destination name に更新し、親が変わる場合は ParentID も destination parent に付け替える。HardLink entry 自身の移動・改名ではその Namespace entry の ParentID / Name を変更し、Target の HardLinkedFile の親子関係や実体データは変更しない。File / Symlink / HardLinkedFile direct entry / HardLink / Directory では永続化・参照関係が異なるため種別別に処理する。Directory 移動では自身または子孫の下への移動を拒否し、親 Directory NLink は children から導出する。destination が既存の場合の置換条件、source / destination Kind の組み合わせ、POSIX errno 詳細は Rename 実装前に契約を確定する。

#### 内部処理候補

| 関数 | 内容 |
|---|---|
| `readInitialObjects` | Distribution の `ListObjects` で ObjectID を列挙し、各 Object の初期 Header データを取得する。`New` から呼び出す |
| `readInitialHeaderData` | 共通 `ReadStripe` API を用いて Header サイズ分の初期データを取得する。Distribution への I/O を担当する |
| `initialize` | `New` から受け取った全 Object の初期データを解析し、Runtime Object を生成する。ParentID / Name から Namespace の親子関係を構築し、HardLink の TargetID を解決して実体参照を共有させた後、`inspect` で構築結果を検査する。Distribution の API は呼び出さない |
| `decodeInitialHeader` | 初期データから Header をデコードし、ObjectID の一致と Header データの妥当性を検証する |
| `buildNamespace` | 初期化対象の Header / Runtime Object から Directory children を構築し、HardLink の TargetID を解決する |
| `inspect` | 初期化後の状態全体を検査する。root を復元できない不整合は初期化エラーとする。一部機能に限定された不整合（壊れた HardLink、ツリー外 Object など）は該当要素を Namespace 上は存在しないものとして warning 報告し、関連 Object / 実体を Cleanup 対象から保護する。検査を複数の公開・非公開関数に分散させない |
| `selectStorages` | 配置方針に使用する Storage を選択する |
| `planPlacement` | データサイズ・容量・状態・配置ポリシーから配置計画を作成する |
| `getStorageCapacity` | Storage の容量情報を取得する |
| `splitWriteRange` | Object の書き込み範囲をストライプ単位に分割する |
| `readStripes` | 必要なストライプを読み込む |
| `writeStripes` | 必要なストライプを書き込む |
| `updateState` | Distribution での永続化成功後に Orchestration の State を更新する |

配置計画を Distribution に渡す具体的な Request 型・フィールド・チャンク枠との対応関係は未確定。Orchestration がチャンク内部情報を持つ設計にはしない。Distribution は配置計画を実際のチャンク配置へ対応付ける。

### 9.5 FUSE callback と Orchestration の対応

FUSE callback は OS ABI / go-fuse の契約に従う。FUSE 層に Namespace / metadata state を複製しない。

| FUSE callback / operation | Orchestration との対応 |
|---|---|
| \`Lookup\` | \`Lookup\` |
| \`Getattr\` | \`GetAttr\`。Symlink 自身の属性を返す lstat 相当 |
| \`Access\` | \`GetAttr\` 属性と request credentials を用いて FUSE 側判定。専用 API 不要 |
| \`Readdir\` | \`ReadDir\` |
| \`Open\` / \`Read\` / \`Write\` | \`Open\` と handle-backed Read / Write。kernel offset を尊重 |
| \`Flush\` / \`Fsync\` / \`Release\` | 同名 Orchestration 操作。lifecycle semantics は Section 11 |
| \`Create\` / \`Mkdir\` / \`Unlink\` / \`Rmdir\` / \`Rename\` | 同名操作。Rmdir / Rename は現状未実装 |
| \`Link\` / \`Symlink\` / \`Readlink\` | \`Link\` / \`Symlink\` / \`ReadLink\` |
| \`Setattr\` | \`SetAttr\`。truncate / metadata update |
| \`Statfs\` | Orchestration の容量スナップショット取得 API（追加が必要） |
| \`Fallocate\` / \`Lseek\` | \`Allocate\` / \`Lseek\`（未実装） |
| \`Opendir\` / \`Readdir\` / \`Releasedir\` | regular-file handle と分離した Directory handle 契約が必要 |
| \`Mknod\` / \`Tmpfile\` | サポート方針と Orchestration Object / handle 契約が未確定 |
| \`Getlk\` / \`Setlk\` / \`Setlkw\` | advisory lock の仕様・実装・テストまで mount option を有効にしない |

\`Statfs\` の total / free / available capacity は Orchestration が I/O / Distribution から取得・集約した値を基にする。値の意味（物理合計、冗長化後の論理容量、利用可能容量など）と更新タイミングは容量 API の契約に明示し、FUSE adapter が独自に推定しない。

### 9.6 未決事項・今後決めること

- Storage 情報の下位 API の具体的な名前・データ型、容量スナップショットの更新・失効タイミング。
- Orchestration が作成する配置計画の具体的な形式、および Distribution のストライプ構成・チャンク枠との対応付け。
- 最後の HardLink 削除時の HardLinkedFile の解放条件、オープン中ファイルの寿命、および参照数の再構築方法。
- ハードリンク作成・削除など複数 Object の永続化を伴う Namespace 操作の原子性とクラッシュ復旧手順。
- Namespace の部分的不整合は \`protectedObjects\` に ObjectID を保持し、Orchestration Cleanup から Distribution Cleanup へ除外対象として渡す。Distribution Cleanup は除外対象 Object の orphan / version / chunk を削除しない。Warning は ObjectID と理由を持つ初期化時の揮発状態として記録する。
- ストレージ障害・容量変化を踏まえた配置計画の再評価と、既存チャンクの再配置方針。
- \`New\` 時の利用可能 IO の判定基準と、非公開状態フィールドおよび getter の具体的な名前・戻り値の構造。
- \`GetErrorStats\` の Request / Response の具体的なフィールド構成と、定期ログ出力の間隔・出力内容。
- 上位層向けエラーコードの一覧と、内部診断情報との分離方法。
- ストライプ書き込みの原子性をプロセス停止・電源断などのクラッシュ時にも保証する具体的な確定・復旧手順。
- \`Rename\` の destination 置換条件、source / destination Kind 組み合わせ、エラーコード、複数 Header 更新時のクラッシュ復旧契約。
- \`Rmdir\` と \`Unlink\` の内部共通 removal primitive をどこまで共有するか。公開 API と POSIX semantics は別操作として維持する。

## Symlink の POSIX 契約

- Symlink Object 自体の属性取得はリンク自身の属性を返す（POSIX の `lstat` 相当）。参照先の属性に置き換えない。
- Symlink のリンク先文字列取得は `ReadLink`（POSIX の `readlink` 相当）として提供し、保存されたパス文字列をそのまま返す。API 名は Go の PascalCase に合わせて `ReadLink` に統一し、`Readlink` は使用しない。リンク先を Orchestration 内部で展開・解決しない。
- Symlink を介したパス参照、およびリンク先の絶対パス・相対パスの解決は POSIX/FUSE のパス解決に委ねる。Orchestration の Namespace root を POSIX の `/` と同一視してはならない。
- Orchestration の Namespace 参照 API は ObjectID / 親子関係を参照するものであり、symlink の追跡や循環検出を独自に実装しない。FUSE 層は通常のパス参照とリンク自身の属性取得を区別する。
- Initialize は symlink のリンク先データだけを `ReadStripe` で先読みし、Runtime Object にキャッシュする。`ReadLink` はキャッシュを返して追加 I/O を行わない。リンク先データが欠落・破損している symlink は Namespace から除外して保護し、他の Object の初期化は継続する。
- HardLink は Symlink と異なり、リンク先文字列を持たない。HardLink の属性およびデータ参照は共有する HardLinkedFile 実体に基づく。

## 9. テスト

- 実装した操作・機能には、対応するテストを用意する。
- レイヤーごとに独立してテストできる構成を維持する。
- 内部実装の詳細だけでなく、公開 API の契約と外部から観測できる振る舞いを検証する。
- 振る舞いや契約を変更した場合は、関連するテストも更新する。

### Go コードのフォーマット

- Go ソースコードは必ず `gofmt` でフォーマットする。
- Go ファイルを編集・生成したら、**ユーザーに確認や手作業を依頼せず、作業者自身が実行環境で**変更対象の Go ファイルへ `gofmt -w` を実行する。フォーマッターを実行できる環境がある場合、実行せずに「整形が必要」と報告して作業を終えてはならない。
- `gofmt -w` の実行後、変更対象の Go ファイルに対して `gofmt -l` を実行し、出力が空であることを確認する。出力がある場合は整形を再実行し、空になるまで確認する。
- フォーマット後は差分を確認し、整形以外の意図しない変更が含まれていないことを確認する。
- `gofmt` を実行できない場合に限り、その理由と試した実行方法を明示する。ユーザーへ手作業を依頼する前に、利用可能な別の実行環境・取得手段を探す。
- フォーマット確認はコミット前の必須ゲートとする。未フォーマットの Go ファイルをコミットしない。

## 10. Pull Request の運用

- 作業を開始する前、および新しい Pull Request を作成する前に、リポジトリに Open 状態の Pull Request が残っていないか確認する。
- Open 状態の Pull Request があり、今回の変更がその作業に関連する場合は、新しい Pull Request を作成せず、既存 Pull Request のブランチに追加コミットする。
- 既存 Pull Request と今回の変更が無関係で、独立した作業として扱うべき場合に限り、新しい Pull Request を作成する。
- 既存 Pull Request が複数ある場合は、変更内容に対応する Pull Request を選ぶ。判断できない場合は、作業を進める前に確認する。
- Pull Request を作成した後も、その Pull Request が Open の間は、同じ作業に関する修正・追加を同じブランチに積む。

## 11. FUSE open / file-handle lifecycle and synchronization

The FUSE adapter uses `github.com/hanwen/go-fuse/v2 v2.12.0`. When implementing or changing FUSE file operations, follow the callback contracts and capability semantics of this pinned version rather than introducing an independent FUSE lifecycle.

### 10.1 File open and handle ownership

- A regular-file open is represented by go-fuse `fs.NodeOpener.Open(ctx, flags)`. It is optional in go-fuse, but FlexRAID requires a non-nil Orchestration file handle for regular-file Read / Write operations. Therefore the FUSE adapter must implement Open and return the handle created by Orchestration.
- The Orchestration package owns the concrete FileHandle type and its private state. The FUSE package only passes that handle through; it must not duplicate cursor, dirty-stripe, or logical-file state.
- go-fuse's `fs.FileHandle` is an opaque `any`. If Open is not implemented or returns no handle, `NodeReader` / `NodeWriter` can receive nil. This is not allowed by the FlexRAID contract: Read / Write without a valid handle must fail rather than silently fall back to ObjectID-only I/O.
- Prefer implementing the go-fuse `fs.FileReader`, `fs.FileWriter`, `fs.FileFlusher`, `fs.FileFsyncer`, and `fs.FileReleaser` interfaces on the Orchestration handle (or thin FUSE adapters around Orchestration operations). Do not implement both inode-level and handle-level variants for the same operation without a deliberate dispatch reason.
- `fs.NodeCreater.Create` and `fs.NodeTmpfiler.Tmpfile` also return a FileHandle. They must create/obtain the same kind of Orchestration handle as Open when those operations are supported; they must not create a handle with different lifetime or synchronization semantics.
- Each successful independent Open creates an independent FileHandle and virtual cursor. A duplicated descriptor (dup) shares the same open-file description and therefore the same FUSE handle; do not create another Orchestration handle for dup.
- The offset supplied to Read / Write is authoritative. The per-handle virtual cursor is only a write-buffer locality hint; it is not a replacement for the kernel-provided offset and must not impose sequential-only I/O.

### 10.2 Flush, Fsync, and Release

- go-fuse `NodeFlusher.Flush` / `fs.FileFlusher.Flush` may be called more than once for the same FileHandle, including when a descriptor was duplicated. Flush must therefore be safe to call repeatedly and must not release the handle.
- Flush is the place to report errors from work required by close(2), because the kernel ignores the return value of Release. Any Dirty Stripe data that must be persisted before close must be committed by Flush, with failures returned there.
- go-fuse `NodeFsyncer.Fsync` / `fs.FileFsyncer.Fsync` represents the request to flush data to stable storage. FlexRAID must persist all pending dirty data covered by the handle before reporting success.
- go-fuse `NodeReleaser.Release` / `fs.FileReleaser.Release` is final handle cleanup. It must be idempotent/robust against teardown and must not be the only place where fallible persistence occurs. Release invalidates the handle; subsequent Read / Write / Flush / Fsync through it must fail.
- Do not treat Flush, Fsync, and Release as synonyms: Flush can repeat, Fsync is an explicit durability request, and Release is final resource cleanup.

### 10.3 Other callbacks associated with an open handle

- `fs.NodeGetattrer.Getattr` may receive a FileHandle parameter, but go-fuse documents that the kernel normally does not send a handle even for fstat. GetAttr must therefore work without one and must not depend on open-specific state.
- `fs.NodeSetattrer.Setattr` may receive a handle. Attribute changes such as size changes (truncate) must be synchronized with data I/O and must preserve the logical Header / file-size invariants. Do not assume a handle is always present.
- `fs.NodeLseeker.Lseek` / `fs.FileLseeker.Lseek` is for `SEEK_DATA` / `SEEK_HOLE` semantics. Ordinary file-position movement is represented by the offset passed to Read / Write; FlexRAID does not need a separate stateful Seek cursor.
- `fs.NodeAllocater.Allocate` / `fs.FileAllocater.Allocate` is a space-allocation operation, not a substitute for Write. If supported, it must synchronize with file-size and stripe allocation changes.
- Directory handles are separate from regular-file handles. `NodeOpendirer.Opendir` is a validation-only callback; `NodeOpendirHandler.OpendirHandle` can return a directory FileHandle and flags. Directory stream callbacks (`Readdir`, `Readdirent`, `Seekdir`, `Releasedir`) must not reuse the regular-file FileHandle contract.

### 10.4 Lock callbacks and mount options

- go-fuse `fuse.MountOptions.EnableLocks` asks the kernel to forward advisory file-lock requests to FUSE. When enabled, implement the corresponding `Getlk`, `Setlk`, and `Setlkw` callbacks (on the inode or FileHandle as appropriate). This is POSIX/application-visible advisory locking; it is not the internal lock that protects FlexRAID's data structures. Do not enable it until those callbacks and their lock-owner/range semantics are implemented and tested.
- `fuse.MountOptions.SyncRead` disables asynchronous reads for a FileHandle: the kernel sends at most one read request per handle at a time and orders them by offset. When unset (the default), concurrent reads and readahead requests may reach userspace. This option only serializes reads per handle; it does not serialize reads against writes, different handles, metadata operations, or other files. FlexRAID must remain correct with the default asynchronous-read behavior; do not use SyncRead as a substitute for Orchestration synchronization.
- Do not set `fuse.FOPEN_PARALLEL_DIRECT_WRITES` in Open flags. It opts into parallel direct writes for that open handle and would bypass the conservative write serialization expected by the initial implementation.
- Do not set `fuse.FOPEN_DIRECT_IO` or `fuse.FOPEN_KEEP_CACHE` without an explicit data-cache policy. DIRECT_IO changes page-cache behavior; KEEP_CACHE asks the kernel to retain cached file contents across opens. Cache coherency must be designed before either is enabled.
- Keep `fuse.MountOptions.ExplicitDataCacheControl` disabled unless FlexRAID explicitly implements and tests cache invalidation/notification. Enabling it transfers responsibility for data-cache invalidation to the filesystem.
- `fuse.FOPEN_NOFLUSH` must not be used because FlexRAID relies on Flush to report errors while committing pending dirty data.
- These options do not provide a general inode-wide mutex. Internal synchronization remains an Orchestration responsibility, keyed by the shared Runtime file object so HardLinks coordinate through the same backing object. The initial implementation should serialize conflicting mutations and ensure reads observe the latest logically committed or dirty data; it must not hold a file lock for the entire open lifetime.
- Advisory POSIX locks enabled through EnableLocks and internal consistency locks are separate mechanisms. One must never be used as a replacement for the other.

### 10.5 Initial FlexRAID policy

- Require a valid Orchestration FileHandle for every regular-file Read / Write.
- Keep one independent virtual cursor per FileHandle; use the kernel-provided offset for every operation.
- Keep dirty-write buffering and logical file state in Orchestration, not in the FUSE adapter.
- Preserve go-fuse's default asynchronous read capability and do not opt into parallel direct writes.
- Do not enable advisory lock forwarding, Direct I/O, KEEP_CACHE, or explicit data-cache control until their complete behavior is implemented and tested.
- Implement Flush, Fsync, and Release with their distinct go-fuse lifecycle semantics described above.

### 10.6 Orchestration handle and stripe-cache ownership

- `FileHandle` is created for each independent `Open` and stores only open-specific state: the shared runtime-file pointer, closed state, access mode, append/sync flags, and a per-handle active write-stripe hint.
- The runtime file backing is created once per data-bearing ObjectID (`File` or `HardLinkedFile`). Every `HardLink` targeting the same `HardLinkedFile` must reference that same runtime-file instance.
- Stripe data cache belongs to the runtime-file backing, never to an individual `FileHandle`. The cache is keyed by StripeID and is shared by all handles referring to the same backing ObjectID.
- Use one per-stripe cache entry type for both read-loaded / read-ahead data and modified data. Its state distinguishes clean data (matching persisted data) from dirty data (modified and not yet persisted). Read-loaded and read-ahead entries are clean; Write transitions the corresponding entry to dirty.
- Clean cache entries may be evicted and reloaded. Dirty entries must not be discarded until persistence succeeds.
- Reads must consult the shared stripe cache before Distribution. A read through another handle must observe a dirty cache entry rather than stale persisted data.
- Runtime-file synchronization protects cache map membership, cache entry data/state, and logical file metadata. A write operation is serialized against reads and other writes for the same runtime file in the initial implementation. Do not add a separate `writing` flag or owner-handle ID merely to duplicate mutex state.
- Handle-local locking protects closed state and handle-local cursor hints. Always acquire the handle lock before the runtime-file lock; do not acquire them in the reverse order.
- A read/readahead request populates the same clean cache map. Kernel-issued readahead requests are ordinary handle-backed Reads; do not create a separate read-ahead cache.

- `Write` stages all affected stripes and the Header in temporary copies while holding the handle mutex followed by the runtime-file exclusive mutex. It publishes the staged entries and updated logical Header together only after the entire in-memory operation succeeds; cancellation or read/validation failure must not partially update the shared cache.
- `Write` uses the kernel-provided offset, except `O_APPEND` where EOF is selected under the runtime-file exclusive mutex. Writes extending EOF zero-fill any gap. File size and mtime/ctime are updated in the runtime-file Header and the serialized Header bytes in the shared stripe cache.
- `activeStripeID` is only a per-handle write-locality hint. Until stripe persistence and placement planning are implemented, changing this hint does not flush or discard dirty entries. Dirty entries remain shared and visible to all handles.
- `O_SYNC` / `O_DSYNC` writes must not report success before their required persistence is implemented. Until then, reject such writes explicitly rather than silently treating them as buffered writes.
- The initial Write implementation serializes writes for one runtime file. Do not add a separate writer-owner ID or `writing` flag; the runtime-file mutex is the serialization mechanism.


## 配置計画とチャンクサイズ

配置計画の算出と、論理ファイル offset を StripeID / ストライプ内 offset に対応付ける処理は Orchestration 層の責務とする。Distribution 層は指定された StripeID・ChunkSize・Layout・IO index 配列に従ってストライプを読み書きし、論理ファイルサイズや Header の意味から独自に配置計画を決定しない。

ここでいう配置計画には、次の2つを区別する。

- **論理配置計画**: ファイルサイズに対する StripeID ごとの論理データ範囲、Layout、ChunkSize。
- **物理 IO 配置**: 各 Stripe のチャンクを書き込む IO index の選択。Distribution が保持する既存配置を再利用するか、Orchestration が空き容量に基づいて選択する。

### 論理配置計画の決定タイミング

- 論理ファイルサイズが変化した場合は、成長・縮小のどちらであっても、変更後のサイズを入力として論理配置計画を再計算する。
- `Write` では、書き込みによって EOF が伸びる場合に再計算する。既存サイズ内に収まるサイズ不変の上書きでは再計算しない。
- `Truncate` では、サイズが変化する場合に再計算する。同じサイズへの Truncate は配置計画を変更しない。
- サイズ不変の上書きは、確定済みの Layout / ChunkSize を維持し、書き込み対象 Stripe だけを dirty にする。
- `Flush` / `Fsync` は、Header とメモリ上の stripe cache に既に反映された論理配置計画を使用する。ここで配置計画や ChunkSize を再計算・変更してはならない。
- 配置計画の再計算は Orchestration 内の処理であり、Distribution の永続化成功を契機に再計算するものではない。

### 先頭ストライプ

- 先頭ストライプ（StripeID 0）の物理チャンクサイズは `MinimumChunkSize`（現在 4 KiB）固定とする。
- StripeID 0 の各物理チャンクの先頭 1 KiB は Header 予約領域であり、Header はこの領域に格納する。ファイル本体のデータはその後ろから格納する。
- StripeID 0 の論理ファイル本体容量は Layout によって決まる。
  - Mirror の場合、論理データチャンク数は 1 として数える。複製数は論理容量に加算しないため、容量は 4 KiB - 1 KiB = 3 KiB。
  - XOR Parity の場合、論理データチャンク数を N とし、容量は 4 KiB × N - 1 KiB。
- 現在の `planPlacement` は、ファイルサイズが Header 予約領域を除いた Mirror 容量（3 KiB）以下なら Mirror Layout を選ぶ。それを超える場合は XOR Parity Layout を選び、既定設定ではデータチャンク数 3・パリティチャンク数 1 とする。
- XOR Parity Layout の StripeID 0 は、後続ストライプと同じデータチャンク数を使用する。そのため、StripeID 0 の論理容量は既定設定で 4 KiB × 3 - 1 KiB = 11 KiB となる。3 KiB を超えた時点で直ちに後続ストライプを追加するわけではない。
- StripeID 0 のファイル本体データは、論理ファイル offset 0 から StripeID 0 の Header 予約領域直後へ連続して格納する。

### 後続ストライプと可変チャンクサイズ

- StripeID 1 以降の Layout は StripeID 0 の Header に記録された Layout を使用する。通常ストライプの論理容量は `ChunkSize × DataChunks` とする。パリティチャンクは論理ファイル容量に含めない。
- 後続ストライプのデータチャンク数・パリティチャンク数は現在の配置ポリシーで決まり、ファイルサイズに応じて増減させない。
- 後続ストライプの ChunkSize は 4 KiB の倍数とし、ファイルサイズに応じて決定する。現在の既定上限は 2 MiB（`DefaultChunkSize`）。
- ChunkSize の算出では、まずファイルサイズから StripeID 0 に割り当てる論理データ量を差し引く。残余データ量を `DataChunks × 4 KiB` 単位で収容できるように、必要な 4 KiB ブロック数を切り上げて ChunkSize を決定する。
- 算出した ChunkSize が上限を超える場合は上限値に固定し、残余データ量に応じて後続ストライプ数を増やす。
- 各後続ストライプは同一の ChunkSize / Layout を使用する。各 Stripe の論理データ量はその Stripe の容量以下であり、最後の Stripe は残余データ量だけを保持する。物理チャンクの不足分のパディングは論理ファイルデータに含めない。
- 論理ファイル offset は、StripeID 0 の論理容量を `S0`、後続ストライプの論理容量を `C × N` とすると、`offset < S0` の場合は StripeID 0、そうでなければ `StripeID = 1 + floor((offset - S0) / (C × N))` とする。後続ストライプ内 offset は `(offset - S0) mod (C × N)`。StripeID 0 内では Header 予約領域 1 KiB を加えた位置がストライプデータ上の offset になる。
- Header に保存する通常 ChunkSize は、後続ストライプが存在する場合はその ChunkSize とする。後続ストライプが存在しない場合も 0 を未設定値として使わず、現在の実装では 4 KiB を保存する。

### サイズ変更時のデータ再構築

- サイズ変更によって論理配置計画が変わる場合、Orchestration は旧 Header の幾何情報で既存データを読み、新 Header の幾何情報に従う stripe cache を再構築する。
- 再構築は Stripe 単位で行い、ファイル全体を一つの巨大な byte slice に展開しない。各移行先 Stripe を作るとき、その Stripe の論理範囲と重なる旧ファイル範囲だけを読み込む。
- `Write` による拡張では、旧 EOF から書き込み開始位置までの gap、および新しい EOF までの未書き込み領域をゼロとして扱う。書き込みデータは移行先 Stripe の重複範囲に適用する。
- `Truncate` による拡張では、新たに増えた範囲をゼロとして扱う。縮小では新しいサイズを超えるデータを移行先計画に含めない。
- 再構築が成功した場合に限り、新しい Header と再構築した dirty stripe cache を Runtime file に反映する。再構築中の読み込み・検証・キャンセル等で失敗した場合は、共有 cache と Header を部分更新しない。
- これは現時点でのメモリ上の再構築・公開契約であり、複数 Stripe にまたがる永続化全体のクラッシュ原子性を保証するものではない。現在の `Flush` は dirty Stripe を順に `WriteStripe` し、各 Stripe の永続化成功ごとに clean にする。途中の Stripe で失敗した場合、それ以前に成功した Stripe の永続化は取り消されない。ファイル全体の配置移行をクラッシュ時にも原子的に確定する方式は別途設計課題とする。

### 物理 IO 配置の選択と再利用

- 既に Distribution に物理配置が記録されている Stripe は、`StripeIOIndexes` で取得した IO index 配列を再利用する。配列の順序は Distribution の契約に従い、Orchestration はチャンク内部の種類・番号・配置を解釈しない。
- 既存配置が取得できない場合、または必要なチャンク数と IO index 数が一致しない場合は、`AvailableIOBytes` で各 IO の利用可能容量を取得し、利用可能容量の大きい順に必要数を選択する。
- 利用可能容量が同じ IO は元の IO index 順を維持し、選択結果を決定的にする。
- 必要な IO index 数は Mirror では複製数、XOR Parity ではデータチャンク数 + パリティチャンク数とする。余分な IO index を Distribution に渡さない。
- 物理 IO 配置の選択は dirty Stripe の `Flush` 時に行う。論理配置計画の再計算とは別の処理であり、Flush が論理 Layout / ChunkSize を変更することはない。

### テスト対象

- Mirror / XOR Parity の切り替え境界（3 KiB の前後）。
- StripeID 0 の容量境界（Mirror 3 KiB、既定 XOR Parity 11 KiB）の前後。
- 後続 Stripe の ChunkSize 境界、4 KiB 単位の切り上げ、2 MiB 上限、および上限到達後の Stripe 数増加。
- 空ファイル、ちょうど境界のサイズ、最終 Stripe が容量未満の場合。
- サイズ拡張・縮小時の Stripe 単位再構築、拡張 gap のゼロ埋め、縮小後のデータ切り捨て。
- サイズ不変上書きで Layout / ChunkSize が維持され、Flush / Fsync で論理配置計画が再計算されないこと。
- 既存 IO 配置の再利用、IO 配置がない場合の容量降順選択、同容量時の IO index 順の決定性。

## NLink の扱い

### 基本方針

- NLink は永続化属性ではなく、現在の Namespace 構造から導出する実行時属性とする。
- Header のバイナリ形式に NLink のフィールドを含めない。`MarshalBinary` は NLink を書き出さず、`UnmarshalBinary` は NLink を永続データから復元しない。
- Header の読み込み後、`initialize` / `buildNamespace` が ParentID / Name と HardLink の TargetID から Namespace を復元した時点で、NLink を導出できる状態にする。
- NLink は Namespace の派生値であり、独立したカウンターとして永続化・更新しない。Namespace を変更する操作は親子関係・HardLink 参照関係だけを正しく更新する。NLink の取得時は、その時点の Namespace から導出した値を返す。キャッシュする場合も Namespace と同じ更新単位で一貫性を保てる場合に限り、永続化してはならない。
- Header / Runtime の内部構造に NLink フィールドを持たせない。NLink は Namespace から必要時に導出し、Header や Runtime に保持しない。
- NLink は POSIX の `st_nlink` として FUSE 層へ返す値であり、Namespace 上の名前・参照数を表す。open handle 数や物理チャンク数、Stripe 数とは無関係とする。

### 種類ごとの導出ルール

| Namespace 上の Kind | NLink | 導出方法・条件 |
|---|---:|---|
| Directory | `2 + 直下の Directory 数` | `.` と `..` の 2 に、直下の子 Directory ごとに親 Directory へのリンク 1 を加える。root も同じ式を使う |
| File | 1 | HardLink 化されていない通常ファイルは Namespace 上の名前を 1 つ持つ |
| Symlink | 1 | Symlink 自体の Namespace エントリ数。リンク先の状態には依存しない |
| HardLink / HardLinkedFile | 同一実体を指す Namespace 名の総数 | HardLinkedFile 自身に ParentID がある場合はその直接所属名を 1 と数え、root から到達可能で同じ HardLinkedFile を TargetID で直接参照する HardLink を加算する。HardLinkedFile 自身の GetAttr は Namespace Object として公開される場合にこの値を返す |
| HardLinkedFile | ParentID がある場合は通常の Namespace Object として返す。nil の場合は直接の属性取得対象ではない | ParentID がある場合は直接所属名 1 と到達可能な HardLink 数を合算する。ParentID が nil の場合は HardLink 経由でのみ参照される実体として扱う |

- Directory の子として数えるのは Kind が Directory の直接の子だけとする。File、Symlink、HardLink は親 Directory の NLink を増やさない。
- HardLink の NLink は、同じ HardLinkedFile を参照する Namespace 上の HardLink エントリすべてに同じ値を返す。別の HardLinkedFile を参照する HardLink は数えない。
- HardLink の連鎖は認めないため、TargetID の直接参照先である HardLinkedFile を単位として数える。Directory / Symlink は HardLink の対象外とする。
- Namespace から到達できない Object、初期化時に不整合として除外・保護された Object は NLink の計算対象に含めない。
- NLink は 0 を通常の Namespace エントリに返す用途ではない。最後の HardLink が削除された実体は Namespace から参照されなくなり、open handle の寿命・Cleanup の規則に従って解放する。

### 関数ごとの責務と挙動

| 関数 | NLink との関係 | 判定・操作の挙動 |
|---|---|---|
| `header.MarshalBinary` | 永続化しない | NLink をシリアライズ対象から除外する |
| `header.UnmarshalBinary` | 永続データから復元しない | NLink を読み取らない。Namespace 構築後に導出する |
| `New` / `readInitialObjects` / `decodeInitialHeader` | 直接計算しない | Header と Object の初期データを取得・検証する。Namespace の参照数を確定する前段階とする |
| `initialize` | 導出の初期化フロー | Header 読み込み後に Namespace を構築し、構築された Namespace から NLink を導出可能にする |
| `buildNamespace` | 導出元を構築する | Directory children と HardLink の実体参照を解決する。NLink の計算は、この構築結果だけを根拠に行う |
| `inspect` | 計算対象の妥当性を確定する | root の一意性、到達可能性、HardLink の参照先などを検査する。不整合で Namespace から除外した要素を NLink に含めない |
| `GetAttr` | NLink を返す | Kind に応じた上記の式で、現在の Namespace から導出した NLink を返す。Header や HardLinkedFile の保存値をコピーしてはならない |
| `Lookup` / `ReadDir` | NLink を直接計算しない | Namespace の名前・親子関係を読み取る。返すエントリは NLink 導出の対象となる現在の Namespace と一致させる |
| `Mkdir` | Directory NLink に影響する | 新しい Directory を親の children に追加する。新 Directory は NLink 2、親 Directory は直下の Directory が 1 つ増えた結果として NLink が 1 増える。NLink の Header 更新・永続化は行わない |
| `Create` | File NLink に影響する | 新しい File を親の children に追加する。File の NLink は 1。親 Directory の NLink は変わらない |
| `Symlink` | Symlink NLink に影響する | Symlink を親の children に追加する。Symlink の NLink は 1。親 Directory の NLink は変わらない |
| `Link` | HardLink NLink に影響する | 元の File Object を同じ ObjectID の HardLinkedFile に変換し、ParentID / Name は維持したまま新しい HardLink を追加する。元の名前と新しい名前の NLink は 2 となる。追加の HardLink ごとに同じ実体を参照する全 Namespace 名の導出値が 1 増える。親 Directory の NLink は変わらない |
| `Unlink` | File / Symlink / HardLink NLink に影響する | 対象の Namespace エントリを親の children から取り除く。HardLink の場合、同じ実体を参照する残りの HardLink の NLink は 1 減る。最後の HardLink がなくなった実体の削除は open handle の寿命・Cleanup の規則に従う。File / Symlink の削除で親 Directory の NLink は変わらない |
| `Rmdir` | 親 Directory NLink に影響する | 空 Directory の Namespace エントリを取り除く。親 Directory の NLink は直下の Directory が 1 つ減った結果として 1 減る。File / Symlink / HardLink が残る Directory は削除できない |
| `Rename` | Directory の移動・置換時に影響する | 同じ親の中での名前変更では Directory NLink は変わらない。Directory を別の親へ移動すると、移動元の親は 1 減り、移動先の親は 1 増える。File / Symlink / HardLink の移動では親 Directory NLink は変わらない。移動先 Directory の置換を許す場合は、置換される空 Directory の削除分も移動先親の導出値へ反映する |
| `SetAttr` | NLink を変更しない | NLink を SetAttr の入力・変更対象にしない。Mode / UID / GID / Size / 時刻などの属性変更で NLink は変わらない |
| `Open` / `Read` / `Write` / `Flush` / `Fsync` / `Release` / `Truncate` | NLink を変更しない | open handle 数やデータ更新は Namespace の名前・参照数ではないため、NLink に反映しない |
| `ReadLink` | NLink を変更しない | Symlink のリンク先文字列を返すだけであり、NLink に影響しない |
| `Cleanup` | NLink を計算・更新しない | 論理削除済み Object や物理データを整理する。Namespace の参照関係・NLink の判定を Cleanup の都合で変更しない |

### Namespace 更新時の原則

- Namespace 操作の成功条件は、対象の親子関係・HardLink 参照関係が一貫した状態で更新されることとする。NLink の数値を個別にインクリメント／デクリメントして帳尻を合わせてはならない。
- Directory の NLink は children の Kind を数えて導出する。Directory を追加・削除・移動した場合も、children の更新だけで導出結果が変わるようにする。
- HardLink / HardLinkedFile の NLink は、HardLinkedFile の ParentID による直接所属名（存在する場合）と、同じ実体を参照する到達可能な HardLink の数を合算して導出する。HardLink の追加・削除時に参照数カウンターを永続化してはならない。
- Namespace の更新に失敗した場合は、部分的な親子関係・参照関係を公開しない。複数 Object の永続化を伴う操作の原子性・クラッシュ復旧は別途定義するが、その都合で NLink を永続化属性にしてはならない。
- NLink の計算は Orchestration の責務とする。FUSE 層は Orchestration が返す NLink を `st_nlink` に設定し、独自に再計算しない。
