# AGENTS.md

このファイルは、FlexRAID の実装・変更時に守る開発規約と、実装上の不変条件を定義する。利用者向けの概要・利用方法・サポート範囲は `README.md` を参照する。

仕様の変更や、README とこのファイルの記述に矛盾を見つけた場合は、どちらかを暗黙に優先して実装を進めない。コード・テスト・両ドキュメントを確認し、正しい契約を確定して両方を同じ変更で整合させる。未確定事項は推測で固定せず、未決であることを明示する。

## 1. 基本原則

- レイヤーの責務と依存方向を守る。上位レイヤーは下位レイヤーの公開 API を利用し、下位レイヤーは上位レイヤーの内部実装を知らない。
- 仕様・契約を満たすために必要な最小限の抽象化を行う。将来の可能性だけを理由に汎用化しない。
- エラーを握りつぶさず、呼び出し元が操作結果を判断できる形で返す。
- 既存データを誤って破壊する可能性がある変更では、失敗時・中断時・再起動後の状態まで検討する。
- 実装変更には、外部から観測できる振る舞いを確認するテストを追加・更新する。
- コードとテストを変更する場合は、作業開始前に default branch のこのファイルを読む。

## 2. パッケージとファイル構成

主要なパッケージは次のとおり。

- `management/`: 外部 API、レイヤー横断の操作調整・排他制御、管理タスク、監視
- `orchestration/`: Header、論理 Object、Namespace、ファイル offset、配置方針
- `distribution/`: ストライプ、チャンク、冗長化、復旧、物理配置
- `io/`: Go の標準 API を中心とした基本的な物理ファイル操作
- `platform/`: OS・実行環境に依存するファイルシステム情報、デバイス情報の取得およびデバイス制御
- `fuse/`: go-fuse と Management のアダプター
- `pkg/shared/`: レイヤー間で共有する型・定数
- `storage/`: 物理保存形式の共通型
- `main/`: 設定読み込みとマウント起動

- パッケージ境界は責務の境界とする。サブパッケージは明確な責務上の理由がある場合に追加する。
- ファイル名は小文字とし、必要に応じて単語をアンダースコアで区切る。
- ファイルは操作または明確な機能・責務ごとに分ける。責務が曖昧な `utils.go`、`helpers.go`、`common.go` を安易に作らない。
- 公開操作は操作ごとの関数として定義し、複数操作を単一の `Execute` などで振り分けない。
- 操作の実装ファイルには、原則としてその操作の Request 型、Response 型、公開関数を置く。操作固有の補助処理は同じファイルに置き、複数操作で共有する処理は責務が明確なファイルへ置く。
- 公開識別子は PascalCase、非公開識別子は camelCase とする。

## 3. 型と共通定義

- 複数レイヤーで共有する型は `pkg/shared` に定義し、各レイヤーから直接参照する。各レイヤーで型エイリアスとして再公開したり、同じ意味の型を重複定義したりしない。
- レイヤー固有の意味・責務を持つ型は、そのレイヤー内に定義する。
- 複数箇所で使用する上限値・既定値・単位定義は `pkg/shared/config` を唯一の正とし、利用箇所に数値を重複定義しない。
- 公開 API の Request / Response 型は、原則として操作ごと・レイヤーごとに定義する。名前が同じという理由だけで異なるレイヤー間の型を共有しない。
- 実行時の公開 API の Request には `context.Context` を含め、呼び出し元からキャンセル・期限・リクエストスコープの値を伝播できるようにする。初期化専用など、実行時要求ではない API は必要性に応じて判断する。
- 公開 API の Response は、その操作の結果として必要な値だけを返す。内部 map / slice / pointer をそのまま公開して呼び出し側から内部状態を変更できる形にしない。必要に応じてコピーまたは読み取り専用の値へ変換する。
- エラーは呼び出し元が判断できる形で返し、上位層で文脈を追加する場合は `%w` 等で元のエラーを保持する。エラー文字列だけを比較して分岐しない。

## 4. Object と Namespace の不変条件

### ObjectID / ParentID

- `ObjectID` は有効な UUIDv4 とする。生成・文字列解析・バイナリ解析など、外部入力との境界で検証する。
- ObjectID のゼロ値を root、未設定、無効値などの特別な識別子として使用しない。
- nullable な識別子は、ゼロ UUID で代用せず、存在フラグなどで有無を明示する。
- `ParentID` は nullable。root Directory と Namespace に直接所属しない File は nil を持ち得る。
- root は ParentID が nil の唯一の Directory Object として識別する。ParentID が nil の File を root と誤認しない。

### ObjectKind / HardLink / Symlink

- 論理 Object の Kind は Orchestration が所有し、File、Directory、Symlink、HardLink を区別する。
- Directory の children は永続化せず、Object の ParentID / Name から起動時にメモリ上へ構築する。
- File はデータ実体を保持する。直接の Namespace 名を持つ場合は ParentID / Name を持ち、HardLink 経由だけで参照される場合は ParentID が nil になり得る。
- HardLink は Namespace 上の名前を表す Object で、ParentID と File 実体を直接指す TargetID を持つ。HardLink 自身はデータを保持しない。
- HardLink の連鎖、Directory / Symlink への HardLink、Directory 自体の HardLink は認めない。
- HardLink 名は参照先 File と同じ Runtime ファイル実体・属性・inode identity を共有する。名前の追加・削除・移動は、対象パスの Namespace entry に対して行い、別名を誤って削除しない。
- File の直接名が削除されても HardLink が残る場合は File 実体を保持する。最後の Namespace 参照がなくなった場合も、open handle が残っていれば handle の寿命が終わるまで実体を利用可能にする。
- Symlink のリンク先文字列は通常の論理データとして保持し、Header に格納しない。リンク自身の属性はリンク自身の属性として返し、ReadLink は保存されたリンク先文字列を返す。パス追跡・絶対/相対パス解決は POSIX / FUSE のパス解決に委ねる。
- NLink は永続化属性にしない。現在の Namespace と HardLink の参照関係から Orchestration が導出し、FUSE は返された値を使用する。

### Namespace 更新

- Directory entry の追加・削除・移動が成功したら、影響を受ける親 Directory の Mtime と Ctime を同じ操作時刻で更新・永続化する。
- 対象は Create / Mknod / Mkdir / Symlink / Link / Unlink / Rmdir / Rename。Rename が同一親なら一度、異なる親なら移動元・移動先の両方を更新する。
- 失敗した操作および変更のない Rename では親時刻を更新しない。
- ファイル内容や子 Object の属性変更では親 Directory の時刻を更新しない。
- 親時刻更新は Orchestration の Namespace 更新経路で行い、FUSE callback で独自に更新しない。
- Namespace の変更と NLink の導出元は同じ親子関係・参照関係とする。NLink の数値を個別に加減算して永続化しない。
- 複数 Header を更新する Namespace 操作について、クラッシュ時の完全な原子性を保証できない場合は、その制約を隠さず明示する。部分更新を成功状態として誤認させない。

### 永続化モデルと Namespace の対応

Namespace は独立したツリーやデータベースとして保存せず、各 Object の Header を正として起動時に再構築する。

| 論理要素 | 永続化単位 | 永続化する情報 | メモリ上だけに保持する情報 |
|---|---|---|---|
| root Directory | 通常の Directory Object | 有効な ObjectID、Kind、ParentID=nil、属性 | children |
| Directory | Directory Object | ObjectID、Kind、ParentID、Name、属性 | children |
| File | File Object | ObjectID、Kind、ParentID（直接の名前がなければ nil）、Name、属性、および Distribution が管理するデータストライプ | Runtime 状態、open handle、キャッシュ |
| HardLink | 独立した HardLink Object | ObjectID、Kind、ParentID、Name、TargetID | TargetID から解決した File Runtime への参照 |
| Symlink | Symlink Object | ObjectID、Kind、ParentID、Name、属性。リンク先文字列は Header ではなく通常の論理データ | Runtime 状態 |

- ObjectID は永続化された Object の識別子であり、Namespace の親子関係は Header の ParentID / Name で表す。
- Directory の children、HardLink の解決済み参照、NLink、Namespace の検索用構造など、永続 Header / データから再構築できる情報を独立した永続データとして重複保存しない。
- File の実データは File Object に一つだけ保持する。HardLink Object は名前と TargetID のみを保持し、データストライプを複製しない。
- 起動時は永続 Object を読み込み、Header を検証したうえで ParentID / Name から Directory children を構築し、HardLink の TargetID を File に解決する。構築後の Namespace と Runtime の参照関係が、実行中の論理状態となる。
- Namespace の追加・削除・移動では、対象 Object の Header に保持する ParentID / Name 等を更新する。Directory children を永続化したり、親 Directory に子一覧を別途書き込んだりしない。
- File の属性変更は該当 Header を更新し、ファイル内容の変更は Distribution のストライプ API を通じてデータを更新する。サイズ変更で配置計画が変わる場合の移行規則は Orchestration の責務に従う。
- HardLink の追加・削除は HardLink Object 自身の永続化・削除と、参照先 File の ParentID / Deleted 状態の更新で表現する。参照先 File のデータを HardLink ごとに複製しない。
- Namespace 操作が複数 Header の更新を伴う場合、更新順序と失敗時の状態を考慮する。複数 Header をまたぐトランザクションやクラッシュ時の完全な原子性は保証されないため、途中状態を成功として扱ったり、未完了の更新を隠したりしない。
- 論理削除は Header の Deleted 状態で表し、物理削除は Cleanup に委ねる。Namespace から見えなくなったことだけを理由に、open handle が利用中の File 実体や不整合保護対象を削除してはならない。

## 5. レイヤーの責務

### Management

- FUSE および将来の管理 API に対する、ファイルシステム全体の公開窓口となる。FUSE からの通常操作は Management を経由し、FUSE から Orchestration / Distribution を直接呼び出さない。
- ファイルシステム操作 API、管理操作 API、監視 API、制御 API を提供する。ファイルシステム操作は同期的な結果・エラーを返し、長時間の Cleanup / Recovery 等はタスクの開始・進捗確認・キャンセルを扱える形にする。
- **システム全体の実行状態を所有する唯一のレイヤー**となる。Lifecycle は Initializing / Ready / Draining / Maintenance / Failed の5状態とする。停止専用の状態は設けず、Stop は最優先の Exclusive 要求として安全単位の境界で実行する。
- 通常の FUSE / POSIX 操作は Ready でのみ受け付ける。Initializing / Draining / Maintenance では EBUSY、Failed では EIO で拒否する。状態照会は全状態で受け付ける。Release / Releasedir / Forget / Interrupt など、kernel bookkeeping と handle teardown に必要な処理は状態にかかわらず受け付ける。Flush は Release と同一視せず、通常操作として扱う。
- 操作受付判断はロック取得の前後で行う。ロック取得前に状態を確認して不要な待機を避け、必要な Object / メンテナンスロック取得後にも状態と操作前提を再確認する。待機中の状態変更との競合を防ぐため、受付中操作の admission と状態遷移の排他・drain 契約を設け、最終確認後から操作開始まで状態遷移が割り込まないようにする。
- Orchestration と Distribution の双方を統括する。通常のファイル操作では Orchestration を呼び出し、Orchestration が Distribution を呼び出す親子関係を維持する。
- Cleanup、Recovery、Scrub 等のレイヤー横断の管理ワークフローでは、必要な論理判断を Orchestration に依頼し、物理ストライプ処理を Distribution に依頼する。意味上 Orchestration を通すべき通常データ操作を、Management から Distribution へ迂回させない。
- Orchestration / Distribution の内部状態を直接変更しない。各レイヤーが公開する API を通じて操作し、状態の所有者と排他制御の所有者を分離する。
- 通常操作と管理タスクの調整、Object 単位のロック、複数 Object にまたがるロック取得、メンテナンス排他を所有する。全 FUSE 操作を単一の全体 mutex で直列化せず、操作対象に応じたロック範囲で並行性を保つ。
- 操作回数・所要時間・結果などの上位操作メトリクスを集計し、Distribution が提供するドライブ別 I/O 統計などを統合して監視 API から返す。物理 I/O のエラー検出・計数は実際に I/O を実行する下位レイヤーで行い、Management はそれを集約・公開する。
- Background は整合性維持を伴う最適化処理とし、Foreground が存在しないときに安全な有限単位を1つ実行して譲る。Foreground 要求は Background 単位の実行中でも受け付けるが、単位の完了境界まで実行を待たせる。Background の次の単位は待機中・実行中の Foreground より優先しない。Cleanup 等の再構築可能な処理は進捗を永続化せず、起動時に現状を再走査して再構築する。各単位は中断・再実行に安全でなければならない。
- 起動・初期化・停止のライフサイクルを統括する。下位レイヤーの初期化を適切な順序で実行し、Orchestration に Header の読み込み・検証、Namespace 構築・整合性検査を行わせる。Orchestration が返す論理削除済みかつ保護対象外の ObjectID を受け取り、Management 自身の一時的な削除待ちキューを再構築する。全レイヤーの初期化と整合性確認が成功するまで外部操作を Ready として受け付けず、途中失敗時は部分初期化状態を公開しない。
- 起動時に Management が設定を読み込み、各ストレージのマウントポイントと物理ドライブ WWN を一組として保持する。マウントポイントごとに対応する WWN を明示し、自動推測で補完しない。設定内でマウントポイントまたは WWN が重複する場合、WWN を Platform 経由で一意に解決できない場合、対応デバイスを確認できない場合は Ready にせず初期化を失敗させる。Management はこの対応付けを WWN をキーとするドライブ状態管理・電源制御の基礎情報として所有する。
- 各ストレージ設定は FlexRAID の I/O root、物理ファイルシステムの mountpoint、WWN の3項目を必須とする。設定読み込み後、物理ストレージディレクトリの作成や Distribution の初期化など副作用を伴う処理より前に、重複・欠落を検証し Platform の公開 API で WWN を解決する。
- Platform の WWN 解決は Linux の `/dev/disk/by-id/wwn-*` を基準とし、パーティションではなく物理ブロックデバイスを対象とする。別識別子へのフォールバックは禁止する。
- Management は設定済み WWN のみを指定できる `SpinUp` / `SpinDown` API を提供し、実際の OS 固有デバイス操作は Platform の公開 API に委譲する。電源制御の判断・スケジューリングは Management に属し、今回の API 実装自体は自動制御を開始しない。
- Object の論理削除状態は Orchestration が Header に永続化するが、削除待ちキューと Cleanup の実行スケジュールは Management が所有する。キューは永続状態の複製を正とせず、起動時に Orchestration から再構築する。物理削除は Distribution に依頼し、完了後に Orchestration へ論理 Object のメモリ状態の確定を依頼する。

### Orchestration

- Header の定義・生成・解析・シリアライズ・更新、論理 Object と Namespace、ファイル offset と StripeID / stripe 内 offset の変換、配置方針を担当する。
- 通常のファイルシステム操作における論理状態の所有者である。Namespace、Header、Object の論理削除状態、open handle 等を管理する。Object の論理削除状態は Header の Deleted を正とし、Management の削除待ちキューを Orchestration 内に重複保持しない。
- Management が提供する排他制御契約の下で呼び出される。Management が必要な Object ロック等を取得していることを前提とし、Orchestration 自身はレイヤー横断のロック管理・ロック取得を行わない。Namespace の内部 map や Runtime 状態を外部へ直接公開しない。
- 初期化では Header の読み込み・解析・検証、Namespace 構築、HardLink 解決、整合性検査を行う。論理削除済みであり、かつ不整合保護対象ではない ObjectID を Management が削除待ちキューへ登録できるよう、初期化結果として読み取り専用の候補一覧を返す。候補一覧は Orchestration 内部のキュー状態ではなく、永続 Header と初期化時の検査結果から導出する。
- 論理削除操作では Header の Deleted 状態と Namespace の変更を担当する。物理削除完了後に Management から通知を受け、対応する Object のメモリ上の論理状態を確定する。Management の Cleanup キューやスケジューリングには関与しない。
- Distribution の API は論理ストライプ単位で利用する。チャンク番号、StripeVersion、物理配置の内部表現を持ち込まない。
- 通常の書き込みは変更対象ストライプだけを読み取り・変更・書き戻す。通常書き込みのためにファイル全体を RAM に読み込んで再構築しない。
- ファイルサイズ変更などで配置計画が変わる場合は、必要な既存データを新しいストライプ境界・チャンクサイズへ移行する。サイズを変えない上書きでは配置計画を再計算しない。
- Stripe 0 の先頭 1 KiB は Header 予約領域。Stripe 0 の物理チャンクサイズは 4 KiB 固定。論理データチャンク数を N とすると Stripe 0 のファイル本体容量は `4 KiB × N - 1 KiB`。
- Stripe 1 以降のチャンクサイズ C は Stripe 0 の Header に保持し、通常ストライプの論理容量は `C × N`。ファイル offset から StripeID / stripe 内 offset への変換はこの geometry に従う。
- Namespace 初期化では Header を読み込み・検証し、ParentID / Name からツリーを構築し、HardLink を解決してから全体を検査する。root を一意に復元できない不整合は初期化エラー。一部 Object の不整合は Namespace から除外して warning を記録し、関連データを誤って Cleanup しないよう保護する。
- 配置先の選択は容量・利用可能状態・ファイルの配置計画に基づき Orchestration が行う。既存ストライプは Distribution が保持する配置情報を再利用する。

### Distribution

- 論理ストライプ単位で読み書きする。論理ファイル offset、ファイルサイズ、Namespace、Header の形式を扱わない。
- `WriteStripe` は指定された Layout / ChunkSize / I/O 配置方針に従ってストライプをチャンクへ分割し、必要な冗長データを生成して永続化する。ファイル内 offset の分割や既存ストライプとのマージは行わない。
- ストライプのバージョン・チャンク・物理配置情報を管理し、起動時に I/O の全件列挙結果からメモリ上へ再構築する。永続インデックスを別途持たない。
- ストライプ書き込みは、ストライプごとに単調増加する Version を割り当て、新しい Version のチャンクを個別の物理ファイルとして順次書き込む。全チャンクの書き込みが完了した場合に限り、その Version を実行中の最新有効 Version として公開し、成功を返す。
- 書き込み途中で失敗した場合、その Version の書き込み済みチャンクは物理的に残り得るが、実行中の Distribution では recoverable ではない候補として扱い、現在選択中の有効 Version を切り替えない。失敗した部分書き込みを成功として返したり、既存 Version を削除したりしない。
- Version はストライプ単位で管理する。ファイル全体や複数ストライプをまたぐ Version 切り替え・トランザクションは行わず、異なるストライプで最新 Version が異なることを許容する。
- 起動時は物理チャンクを ObjectID / StripeID / Version ごとに集約し、形式・識別子・重複チャンクなどを検証する。不正な候補 Version は復旧候補から除外する。各候補を実際に復元可能か検証し、復元可能な候補のうち最も大きい Version をそのストライプの最新有効 Version として選ぶ。
- 起動時の復旧判定では、以前の実行で書き込み途中に失敗した Version であっても、残存チャンクだけで復元可能なら選択対象になり得る。逆に、より新しい Version が復元不能なら、それを選択せず、復元可能な最も古い Version ではなく「最も新しい復元可能 Version」へフォールバックする。復元可能な Version が一つもなければ、そのストライプは利用できない。
- Mirror は有効な複製チャンクが1つ以上あれば復元可能とする。XOR 1 パリティは、全データチャンクがあればパリティチャンクが欠落していても復元可能とし、データチャンクが1つだけ欠落している場合はパリティチャンクを使って復元する。データチャンクが2つ以上欠落している場合は復元不能とする。
- 読み込み時は選択済み Version の各チャンクを再読込し、物理読み取りエラーや不正なチャンクを欠落として扱って復元を試みる。復元できなければデータ破損エラーを返す。欠落チャンクを復元できた場合は、呼び出し元から修復配置が与えられていれば同じ Version の欠落チャンクを修復する。修復は新しい Version のコミットではない。
- 復旧はチャンクの欠落または物理読み取りエラーにより取得できない場合を対象とする。読み取れたデータ同士の内容比較や、全データチャンクが読めた場合のパリティ不一致だけを理由にデータを訂正しない。チェックサム等によるサイレント破損・改ざん検出は保証しない。
- Distribution は StripeID ごとの状態・物理チャンク候補を所有し、同一 ObjectID + StripeID を最小単位とする Stripe ロックで Read / Write / Recovery / Cleanup の競合を防ぐ。ロック取得後に削除対象 Version・Chunk がまだ不要であることを再検証し、物理削除と Distribution 内部状態の更新を一貫して行う。
- Cleanup は旧 Version、不正・復元不能 Version、未完了チャンク、孤立チャンク、および論理削除 Object の物理チャンクを対象にする。Object 全体の削除では Stripe 単位で処理し、Header を含む Stripe 0 は他の Stripe の削除完了後に最後に処理する。Management は ObjectID 単位の論理削除済み Object の処理待ちを管理し、Distribution は Stripe / Chunk 単位の物理削除候補と処理結果を管理する。全 Stripe の削除完了を確認してから Management 経由で Object の削除完了を Orchestration に反映する。
- Cleanup の候補追跡・物理削除の整合性は Distribution が担い、いつ・どれだけ実行するかというスケジューリングは Management が担う。長時間の全件削除を通常操作の呼び出し内で無制限に実行せず、Stripe などの有限単位で進められる API を提供する。
- 初期パリティ方式は XOR 1 パリティであり、1 データチャンクの欠損まで復元できる。内容改ざん・サイレント破損の検出を保証しない。
- 配置ポリシーは決定せず、Orchestration から渡された配置方針を実際のチャンク配置に反映する。
- ドライブ別 I/O エラー統計など Distribution が取得できる物理層の観測値を、Management が問い合わせ・集約できる公開 API として提供する。統計値は内部 map を共有せず snapshot として返す。

### I/O

- 指定された物理 root 上で物理ファイルの読み書き・同期・削除・列挙を行う。
- Header の解析、チャンクやストライプの意味解釈、配置方針、パリティ計算、復旧判断を行わない。
- `ScanHeaders` は StripeID 0 の物理ファイル列挙、`ListChunks` は全ストライプの物理ファイル列挙を行う。どちらも Header / Chunk の意味を解析しない。
- ディスクの電源状態やスピンダウン・スピンアップを判断するポリシー、Cleanup のスケジューリングを持たない。必要な物理操作を実行し、その結果を上位層へ返す。

### Platform

- OS・実行環境ごとの差異を吸収し、FlexRAID が必要とする環境情報の取得およびデバイス制御 API を提供する。
- ファイルシステム容量など OS 固有 API を必要とする情報、物理ドライブの識別・対応付け、ドライブの電源状態取得、スピンアップ / スピンダウン要求などを担当する。
- Go の標準 API で実現できる基本的な物理ファイルの読み書き・同期・削除・列挙は担当せず、I/O パッケージの代替やラッパーにはならない。
- I/O と Platform は独立した兄弟コンポーネントとし、相互に依存しない。Platform から I/O を呼び出さず、I/O から Platform を呼び出さない。
- Platform は状態取得・デバイス制御の機構を提供する。スピンダウンの判断、電源状態の運用管理、処理スケジューリングは Management が担当する。
- Platform は OS 固有実装を隔離する境界とする。OS ごとの差異は Platform 内の実装で吸収し、上位層には共通 API と共通の結果型を提供する。

### FUSE

- go-fuse v2.12.0 の callback 契約に従い、OS からの操作を Management API へ変換する。Namespace や論理属性の独自キャッシュを持たない。
- FUSE は物理ファイル・チャンク・パリティ・配置方針を直接扱わない。
- regular file の Read / Write は有効な Management 経由の FileHandle を必須とする。独立した Open ごとに handle を作り、dup は同じ open-file description / handle を共有する。
- Read / Write の offset は kernel から渡された値を正とし、handle 内の cursor を位置の正として扱わない。
- Dirty stripe cache は FileHandle ごとではなく Runtime file 実体ごとに共有し、HardLink 間でも同じ cache を参照する。キャッシュの所有者は論理ファイル状態を管理する Orchestration とし、Management / FUSE が独自の複製を持たない。
- Flush は複数回呼ばれ得るため反復可能とし、handle を解放しない。Fsync は pending dirty data の永続化を保証する。Release は最終的な handle cleanup とし、失敗し得る永続化を Release だけに依存させない。
- FUSE の `default_permissions` に OS の権限判定を委ねる。FlexRAID 独自の permission 判定を重複実装しない。
- Advisory lock forwarding、Direct I/O、KEEP_CACHE、ExplicitDataCacheControl、parallel direct writes は、対応する整合性契約とテストが揃うまで有効化しない。

### レイヤー間の依存関係

レイヤーの呼び出し・依存方向は次の DAG とする。矢印は呼び出し元から呼び出し先への依存を表す。

```text
FUSE -> Management
Management -> Orchestration
Management -> Distribution
Management -> Platform
Orchestration -> Distribution
Distribution -> I/O
Distribution -> Platform
```

- Management は Orchestration と Distribution の双方を統括するが、Orchestration と Distribution の親子関係は維持する。通常のファイルシステム操作は Management -> Orchestration -> Distribution -> I/O の経路を通る。
- Platform は I/O と同列の独立コンポーネントであり、Management と Distribution など必要な上位コンポーネントから利用する。I/O と Platform の間に依存関係を設けない。
- Distribution は Platform から OS 固有のファイルシステム容量・デバイス情報を取得できる。Management は Platform を通じてドライブ状態の観測・制御を行う。Platform はそれらの判断ポリシーを持たない。
- 現行の `io.GetCapacity` は `unix.Statfs` で容量を取得している。目標構成では、このような OS 固有の環境情報取得を Platform に分離し、I/O は Go の標準 API を中心とした基本ファイル操作に専念させる。
- Management から Distribution への直接呼び出しは、管理タスク・監視など、論理ファイル操作を必要としない責務に限る。通常のファイルデータ操作を Orchestration 経由から迂回させない。
- 下位層は上位層の型・状態・実装を参照しない。下位層から上位層への callback で依存グラフに逆向きの辺を作らない。結果・統計は下位層の公開 API を通じて上位層が取得する。
- 依存関係は DAG とし、循環依存を作らない。

### 現行実装からの移行

- Management は今後導入するレイヤーであり、現時点の実装は FUSE が Orchestration を直接呼び出し、Orchestration が自身の mutex で操作を排他している。この状態は移行前の実装であり、目標アーキテクチャを変更する理由にはしない。
- Management 導入時は FUSE の接続先を Management に変更し、Orchestration のグローバル mutex に依存する契約を Management 所有の操作スコープ別ロックへ移行する。移行中に Orchestration のロックを単に削除して競合保護を失わせてはならない。
- Distribution の Stripe ロックは Distribution 自身が所有する。Management / Orchestration が Distribution の内部ロックを直接操作しない。

## 6. 制約値とファイルシステム属性

- 共有制約値は `pkg/shared/config` を唯一の正とする。現在の主要な制約には、最大ファイルサイズ 16 TiB、最大名前長 255 bytes、BlockSize 4 KiB、Header 予約領域 1 KiB がある。
- 単位を混同しない。I/O 容量は bytes、StatFs のブロック数は BlockSize 単位とする。
- StatFs は Orchestration が組み立て、FUSE は Linux statfs response へ写像する。物理総容量・空き容量と、冗長化を考慮した論理配置可能容量を区別する。
- FUSE の NLink / inode identity / mode / ownership / timestamps / size は Orchestration の論理属性と一貫させる。

## 7. 排他制御

### Lifecycle と実行キュー

- Lifecycle は Management が所有し、状態は Initializing / Ready / Draining / Maintenance / Failed の5つとする。
- Ready で通常操作を受け付ける。Initializing / Draining / Maintenance は通常操作を EBUSY で拒否し、Failed は EIO で拒否する。Status は全状態で受け付ける。
- Ready で最初の Exclusive 要求を受け付けた時点で Draining に遷移し、新しい通常操作を拒否する。受付済みの通常操作と実行中 Background unit の完了を待って Maintenance に遷移する。
- Exclusive 要求は FIFO キューで管理する。Stop は最優先とし、キュー先頭へ挿入する。Maintenance 中に次の Exclusive が待機している場合は Ready に戻さず連続して実行する。キューが空になった場合だけ Ready に戻る。
- Initializing 中の通常 Exclusive 要求はキューへ登録し、初期化完了後に実行する。初期化中は Ready を経由しない。Stop は初期化を非同期に破壊せず、現在の初期化安全単位の完了境界で初期化を中断して停止処理へ移る。
- Failed では通常 Exclusive を拒否する。Stop と Status のみ受け付ける。Failed 中に実行中の Exclusive があれば安全単位の境界で中断する。
- Stop は通常操作の新規受付を閉じ、実行中の通常操作・Background unit・Exclusive unit の安全な完了を待つ。Stop 自身も同じ実行権・ロック調整機構を使い、別系統の停止ロックを設けない。
- Release / Releasedir / Forget / Interrupt 等の kernel bookkeeping と handle teardown は Lifecycle の通常操作 admission から除外し、停止・Failed 状態でも実行可能にする。Flush は通常操作であり、Release の代替として扱わない。

### Foreground / Background / Exclusive の実行権

- Foreground は複数同時実行を許可する。Background は一度に1 unitだけ実行し、Foreground が待機中または実行中なら次の unit を開始しない。
- Foreground 要求が Background unit の実行中に到着した場合、要求を受け付けてキューへ登録する。実行中 unit は強制中断せず、安全境界まで完了させ、その後 Foreground を優先する。
- Background unit は有限時間で完了し、クラッシュしても整合性を保てる安全単位とする。unit 完了後にのみ Foreground / Exclusive / Stop へ実行権を譲る。
- Background は状態を再走査すれば作業対象を再構築できる最適化処理に限定する。進捗・カーソル・チェックポイントの永続化を必要とする処理は Background の小分け実行にしない。そのような処理は、永続化境界を含む一括バッチとして設計する。
- Background の中断・再起動で失われるのはメモリ上の走査位置だけとする。再起動時に現状から対象を再構築し、同じ unit が再実行されても安全な冪等性、または同等の再実行安全性を持たせる。
- Exclusive は通常操作と Background の新規実行を止め、既存の安全単位・受付済み操作が完了してから実行する。複数 Exclusive はキュー順に連続実行する。
- Initialization も安全単位に分割し、Lifecycle の制御下で実行する。初期化中の Stop は unit 境界で反映し、それ以外の Exclusive は初期化完了後に処理する。

### Object / Maintenance ロック

- Management は Lifecycle admission と Object ロックを別の同期契約として所有する。Lifecycle の最終 admission 判定と操作開始の間に状態遷移が割り込まないよう、受付登録を原子的に行う。
- Object ロックは ObjectID をキーとし、通常の読み取り同士は共有、書き込み・属性変更・Namespace 変更は排他とする。操作が複数 Object に関係する場合は必要な全 ObjectID を一意に正規化し、一定の順序で取得する。部分取得したまま別 Object のロックを待つ実装にせず、デッドロックを防ぐ。
- Object ロック取得前に Lifecycle を確認して不要な待機を避け、取得後に Lifecycle と操作前提を再確認する。後段の確認に失敗した場合は取得済みロックを解放して下位層を呼び出さない。
- Background unit も Foreground と同じ Object ロック管理を使用する。Background が触れる ObjectID とロックモードを明示し、unit の開始から安全境界まで保持する。Background 専用の別ロック体系を作らない。
- Exclusive / Stop は Maintenance 排他を取得し、通常操作・Background が保持する実行権がなくなるまで待つ。Maintenance 排他を保持している間は通常操作 admission を再開しない。
- ロックの取得順序は Management の Lifecycle admission / Maintenance 排他 / Object ロック / 下位レイヤーの Stripe ロックの順とする。Distribution の Stripe ロックを Management が直接操作しない。下位層から Management のロックを要求する逆方向の依存を作らない。
- Object ロックは無関係な Object の操作を不必要に直列化しない。全 FUSE 操作を一つの全体 mutex で直列化しない。
- Orchestration の公開 API は Management が必要な Object ロックを取得済みであることを前提とし、Orchestration 自身は Management のロックや Lifecycle admission を行わない。
- Distribution は同一 ObjectID + StripeID を最小単位とする Stripe ロックを所有し、Read / Write / Recovery / Cleanup の競合を防ぐ。削除候補を事前調査した場合は Stripe ロック取得後に削除可能条件を再検証する。
- Object 全体の削除では Management が Object 単位の操作を調整し、Distribution が Stripe 単位の排他を取得して物理削除する。Stripe 0 は他 Stripe の削除完了後に最後に処理する。全 Stripe の削除完了後に Orchestration の論理 Object メモリ状態を確定する。
- FUSE callback はアダプターであり、Management / Distribution のロックを直接取得しない。複数の Management API を FUSE 側で組み合わせても原子性は得られない。不可分な操作は Management の単一 API とする。
- go-fuse の SyncRead 等の mount option を内部同期の代替にしない。異なる handle、HardLink、read/write、metadata 操作間の整合性は Management と各データ所有レイヤーのロック契約で保証する。

## 8. ログ

- Go 標準ライブラリ `log/slog` を使用する。
- 公開操作の開始・完了、処理件数、所要時間、結果要約など通常処理の記録は DEBUG とする。
- WARNING は処理を継続できるが運用者が把握すべき例外的状況、ERROR はデータ整合性や可用性に実害を及ぼす重大な問題に限る。
- error を返すことだけを理由に WARNING / ERROR を出さない。呼び出し側で通常処理として扱えるエラーは原則 DEBUG とする。
- 同じ問題を下位層・上位層で重複記録しない。ログレベルは実行時に選択可能とする。
- データ本体、秘密情報、不要に大きな値をログへ出さない。

## 9. テストと品質ゲート

- 実装した操作・機能には対応する単体 / 統合テストを用意し、内部実装だけでなく公開 API と外部から観測できる振る舞いを検証する。
- Go テストは `go test ./...` で実行できる状態を維持する。
- Go ソースを変更・生成したら `gofmt -w` を実行し、続けて `gofmt -l` の出力が空であることを確認する。フォーマット確認はコミット前の必須条件とする。
- POSIX 互換性の回帰確認には、リポジトリで固定した pjdfstest revision と FlexRAID 専用 skiplist を使用する。
- pjdfstest の SKIP は v1 でサポートしない機能に限る。実装済み機能の不具合や単に未実装なだけの機能を隠す目的で追加しない。
- skiplist は `tools/pjdfstest-flexraid-skiplist.tsv` を唯一の正とし、ケース単位で理由を明記する。pjdfstest 更新時は SKIP 対象のテスト内容を再確認する。
- テスト失敗を修正するときは、最初の失敗原因をログと assertion から特定し、関連する回帰テストを追加する。

## 10. Pull Request 運用

- 作業開始時と新規 PR 作成前に Open PR を確認する。
- 今回の変更に関連する Open PR があれば、そのブランチへ追加コミットする。無関係な PR がある場合のみ独立 PR を作成する。
- PR は変更範囲・設計判断・テスト結果を明記する。実装コードを変更した場合は、関連テストと CI 結果を確認する。
- 変更対象外のファイルを整形・更新したり、無関係な変更を同じ PR に混ぜたりしない。

## 11. 構想段階: 管理ユーティリティ（未実装・要件検討中）

> **構想段階のメモ。** 以下は将来追加する管理ユーティリティの方向性と暫定要件であり、現行実装の仕様・確定した CLI 契約・実装着手指示ではない。コマンド名、設定形式、対応 OS などは今後の設計で決定する。物理ドライブ UUID の定義は WWN とする。

- 将来的に、FlexRAID のストレージデバイスを管理するためのユーティリティを追加する。
- Management が起動時に読み込む設定に、各ストレージのマウントポイントと物理ドライブ WWN を一組として記述する。WWN はマウントポイントに対応する物理ドライブを明示するものであり、設定から省略したり実行時の自動推測で補完したりしない。
- デバイス交換時には、交換先デバイスの識別子をコマンド引数として明示的に指定し、対象 I/O ルートとの対応付けを更新できるようにする。交換時に新しい識別子を指定できることを必須要件とする。
- 将来の管理ユーティリティでは、ホストから認識できる対象デバイスとその識別子を列挙し、利用者が一覧から交換先デバイスを選択できるようにする。識別子だけでは候補を区別しにくい場合に備え、モデル名・容量など補助情報を併せて表示する方向で検討する。
- 一覧からの選択と同等に、コマンドから識別子を直接指定する操作も可能にする。対話 UI の利用を必須にしない。
- 指定された識別子が見つからない場合や、一意に解決できない場合は、誤ったデバイスを対象にせず明確に失敗させる。既存の対応付けを暗黙に別デバイスへ切り替えない。
- この用途で必要なのは物理デバイスの識別子であり、Filesystem UUID などファイルシステムの識別情報を代用しない。
- 物理ドライブ UUID は WWN（World Wide Name）とする。Linux では `/dev/disk/by-id/wwn-*` 等で公開される WWN を基準にする。取得できないドライブは識別不能として扱い、シリアル番号など別形式への暗黙のフォールバックは行わない。WWN が重複するなど一意に解決できない場合も操作を拒否する。
- マウントポイントから物理デバイスまでの自動トポロジー解決は、このユーティリティの必須要件に含めない。必要なデバイス識別情報は列挙結果から利用者が選択し、設定へ明示的に反映する方針とする。
- 設定ファイルの具体的なフォーマット、設定更新の原子性、実行中プロセスへの反映タイミング、交換前後の安全確認、デバイスが未接続の場合の扱いは未決事項として今後定義する。

