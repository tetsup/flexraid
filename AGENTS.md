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
- `io/`: 物理ファイル操作・デバイス監視・OS 依存機能を一元提供する唯一の公開窓口
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

#### Lifecycle StateMachine / State Hooks

- Lifecycle の状態と遷移可否は Management の StateMachine が唯一の正として管理する。StateMachine は現在状態・遷移要求・遷移結果を扱い、外部プロセスの起動停止などの副作用を直接実行しない。
- 状態ごとに `OnEnter` と `OnExit` の Hook を登録できる。Hook は `context.Context` を受け取り、失敗を error として返す。状態に依存するバックグラウンドプロセスや監視処理などの開始・終了は、呼び出し元が個別に順序制御するのではなく、該当状態の Hook に登録する。
- 実際の遷移は StateMachine の状態判断と、副作用を実行する Transition Coordinator / Hook Executor を分離して扱う。StateMachine の状態判定ロジックを I/O、goroutine 起動停止、他レイヤー API 呼び出し等に依存させない。
- 遷移元の `OnExit` を実行し、成功した後に遷移先の `OnEnter` を実行する。両 Hook が成功した場合に限り、遷移先状態を確定・公開する。Hook の実行中に遷移先状態を先行して公開しない。
- `OnEnter` が失敗した場合は遷移を成立させず、同じ遷移で既に起動した処理があれば、その処理に対応する終了処理を逆順に実行してロールバックする。ロールバックの失敗も呼び出し元へ伝え、隠さない。
- `OnExit` が失敗した場合は遷移先へ進まず、遷移失敗として扱う。終了処理が一部だけ成功した可能性を考慮し、状態と実プロセスの不一致を黙って通常運用へ戻さない。必要な復旧または Failed への遷移は、明示的なエラー処理として行う。
- 同じ状態に留まる要求では Hook を実行しない。実際の状態変更一回につき各 Hook は一度だけ実行し、並行する遷移要求による二重実行を防ぐ。Hook 実行中に同じ StateMachine の別遷移を並行実行しない。
- Hook は StateMachine 内部の状態 Mutex を保持したまま実行しない。遷移の直列化は別の仕組みで保証し、Hook から Lifecycle / Management API を呼び出しても状態 Mutex の自己デッドロックを起こさないようにする。
- Ready に依存する worker は Ready の `OnEnter` で起動し、Ready の `OnExit` で停止する。初期化処理は Ready への遷移前に完了させる。main や FUSE mount 処理に worker の個別起動・停止順序を重複して持たせず、main は Management の Lifecycle 開始・停止 API を呼び出す。

- 通常の FUSE / POSIX 操作は Ready でのみ受け付ける。Initializing / Draining / Maintenance では EBUSY、Failed では EIO で拒否する。状態照会は全状態で受け付ける。Release / Releasedir / Forget / Interrupt など、kernel bookkeeping と handle teardown に必要な処理は状態にかかわらず受け付ける。Flush は Release と同一視せず、通常操作として扱う。
- 操作受付と状態遷移の競合を防ぐため、admission 登録と Lifecycle 遷移を原子的に調停する。実行権の取得待ちを含め、受付済み操作の開始・完了を追跡し、drain 時には新規受付を止めて必要な実行単位の完了を待つ。
- Orchestration と Distribution の双方を統括する。通常のファイル操作では Orchestration を呼び出し、Orchestration が Distribution を呼び出す親子関係を維持する。
- Cleanup、Recovery、Scrub 等のレイヤー横断の管理ワークフローでは、必要な論理判断を Orchestration に依頼し、物理ストライプ処理を Distribution に依頼する。意味上 Orchestration を通すべき通常データ操作を、Management から Distribution へ迂回させない。
- Orchestration / Distribution の内部状態を直接変更しない。各レイヤーが公開する API を通じて操作し、状態の所有者と排他制御の所有者を分離する。
- Foreground / Background / Exclusive / Initialization / Stop のすべてに共通する、Management 唯一の実行ロック機構を所有する。Management の実行単位は同時に一つだけ実行し、各単位が共通ロックを取得して直列実行する。ObjectID などの対象別ロック、複数対象ロック、別系統のメンテナンスロックは設けない。ロックは復旧可能な最小実行単位の開始時に取得し、その単位の完了・状態確定後に必ず解放する。次の単位へ進む前に実行権を譲り、Lifecycle と Scheduler が実行種別ごとの受付・優先順位・drain を調停する。これはロック粒度を Object や Stripe に広げることを意味しない。
- Management 自身の操作メトリクスは Management が所有・集計する。監視 API の応答を構成するときは、Orchestration・Distribution・I/O がそれぞれ所有するメトリクス snapshot を各層の公開取得 API から直接取得する。下位層のメトリクスを別の下位層へ渡して再集計させない。Management は表示・集約の窓口であり、各層の統計の所有者にはならない。
- Management が Scheduler / MaintenanceExecutor を通じて実行する Background 作業は、Foreground との優先順位を Management の共通実行ロックで調停する。一方、Distribution の自律 Cleanup は Management の Background WorkItem として実行しない。Management は Distribution の物理 Cleanup ループと Orchestration の論理削除 Object Cleanup ループの起動・停止、および Drive ごとの許可情報更新を行う。各ループの処理判断と候補管理は所有レイヤーが行う。Orchestration worker は Management の公開 Cleanup 経路を通じて実行し、Management の操作排他を迂回しない。Cleanup の進捗は永続化せず、起動時・再開時に各所有レイヤーが状態から候補を再構築する。

#### Maintenance Policy / Executor

- Management 内で Maintenance として実行する最適化処理は、実行判断を行う `MaintenancePolicy` と、判断された作業を実行する `MaintenanceExecutor` に分離する。Maintenance は Background 全般の別名ではなく、Management が実行主体となる保守作業を対象とする。Distribution の自律 Cleanup はこの Policy / Executor の対象外とする。
- `MaintenancePolicy` は、各 `MaintenanceWorkProvider` が提示する作業候補、Management が候補情報から集計したメトリクス、I/O から取得したドライブ状態、Foreground の受付・実行状況などの Snapshot を入力とし、`MaintenancePlan` を決定する。候補一覧は作業の出所であり、メトリクスは候補の量・物理的な負荷・実行可能性などを集計した判断材料である。Policy は作業の具体的な実行方法を持たず、Executor は省電力・優先度などの判断ロジックを持たない。Distribution Cleanup の候補選定・実行判断は Management の Policy に持ち込まない。
- `MaintenanceWorkProvider` は自レイヤーが所有する状態から、作業候補と計画・実行に必要な情報を読み取り専用 Snapshot として提供する。Management は候補の所有者にならず、候補一覧やレイヤー内部状態を独立した正として複製しない。候補情報と下位レイヤーの物理情報からメトリクスを集計するのは Management の責務とし、実行時には所有レイヤーの公開 API を使用する。
- `MaintenancePlan` は順序付きの `MaintenanceBatch` と `MaintenanceWorkItem` で構成する。Batch は Policy が決めた作業のグルーピングであり、ロック範囲ではない。WorkItem は Provider ID、Provider が識別する Work ID、および Policy が判断に使った Disk ID 情報を保持する。
- `MaintenanceExecutor` は Plan の Batch / WorkItem の順序を変更せず、各 WorkItem について Provider の最小安全単位を一度ずつ実行する。Provider はその WorkItem が完了したかを返し、未完了なら次回も同じ WorkItem を続ける。Executor は計画外の作業を追加せず、Disk ID に対応するドライブ状態を再取得・再判定せず、SpinUp / SpinDown もしない。
- Management の共通実行ロックは復旧可能な最小作業単位ごとに取得・解放する。Batch 全体や WorkItem 全体でロックを保持せず、各 Provider unit の境界で Scheduler に実行権を返し、待機中の Foreground / Exclusive / Stop を調停する。MaintenanceExecutor は既存 Scheduler の BackgroundTask 契約に適合し、別のロック機構を設けない。
- MaintenancePolicy / Executor が扱う Management 所有の保守作業では、停止中ドライブの扱いを Policy が判断する。Distribution Cleanup のドライブ選択・実行は、別途定義する Drive 単位の Cleanup 許可レベルに従い Distribution 自身が行う。
- ドライブ状態の観測と SpinUp / SpinDown の OS 固有操作は I/O が提供する。Management はドライブ単位の Cleanup 許可レベルを所有し、設定ファイルで初期値を受け取り、公開 API で実行中に変更する。許可情報は不要 Version 等の自律 Cleanup と論理削除 Object の物理削除の双方に適用する。Management は許可情報を各レイヤーへ公開するだけで、削除対象や削除単位を選択・指示しない。許可情報の変更自体は削除処理を開始しない。
- 初期実装に学習、適応的な効率推定、複雑な最適化アルゴリズムを持ち込まない。効率計算・アルゴリズム比較は外部シミュレータで行う。Policy は差し替え可能にし、判断入力・Plan・実行結果を観測できるようにする。

- 起動・初期化・停止のライフサイクルを統括する。下位レイヤーの初期化を適切な順序で実行し、Orchestration に Header の読み込み・検証、Namespace 構築・整合性検査を行わせる。Orchestration は Deleted Header と整合性検査結果から自身の一時的な削除待ちキューを再構築する。Management は `CleanupPermissionStore` の同一インスタンスを Orchestration と Distribution に共有し、更新だけを行う。削除候補の選択・削除ワークフローは各所有レイヤーに委ね、Management は削除キューや ObjectID / StripeID の候補を複製しない。全レイヤーの初期化と整合性確認が成功するまで外部操作を Ready として受け付けず、途中失敗時は部分初期化状態を公開しない。初期化完了後に Lifecycle が Ready へ遷移し、その後 Management が Distribution の自律物理 Cleanup worker と Orchestration の論理削除 Cleanup worker の両方を明示的に起動する。停止時および起動途中の失敗時は、起動済み worker を逆順に停止する。バックグラウンドプロセスの起動が完了するまでは FUSE mount を行わず、外部操作を受け付けない。
- 起動時に Management が設定を読み込み、各ストレージのマウントポイントと物理ドライブ WWN を一組として保持する。マウントポイントごとに対応する WWN を明示し、自動推測で補完しない。設定内でマウントポイントまたは WWN が重複する場合、WWN を I/O 経由で一意に解決できない場合、対応デバイスを確認できない場合は Ready にせず初期化を失敗させる。Management はこの対応付けを WWN をキーとするドライブ状態管理・電源制御の基礎情報として所有する。
- 各ストレージ設定は FlexRAID の I/O root、物理ファイルシステムの mountpoint、WWN の3項目を必須とする。設定読み込み後、物理ストレージディレクトリの作成や Distribution の初期化など副作用を伴う処理より前に、重複・欠落を検証し I/O の公開 API で WWN を解決する。
- I/O の WWN 解決は Linux の `/dev/disk/by-id/wwn-*` を基準とし、パーティションではなく物理ブロックデバイスを対象とする。別識別子へのフォールバックは禁止する。
- Management は設定済み WWN のみを指定できる `SpinUp` / `SpinDown` API を提供し、実際の OS 固有デバイス操作は I/O の公開 API に委譲する。電源制御の判断・スケジューリングは Management に属し、今回の API 実装自体は自動制御を開始しない。
- Management は `pkg/shared` の `CleanupPermissionStore` を唯一の共有状態として所有し、各 Drive の `DeleteAllowed` と `PreserveAllowed` を独立して管理する。初期値は設定ファイルから受け取り、公開 API で実行中に変更する。Management だけが Store を更新し、Orchestration と Distribution は同じ Store の Snapshot を読み取る。許可更新は Management のファイル操作実行ロックを待たずに反映し、論理削除 Object の現在処理中の ObjectID には影響せず、次の ObjectID の開始時から適用する。
- Management は共有 Store を更新する。Distribution は自律 Cleanup の各物理ファイル開始時に Snapshot を参照する。Orchestration は ObjectID の選択時に Snapshot を参照し、Distribution は各 ObjectID の処理開始時に改めて Snapshot を取得して必要な全 Drive が許可されていることを確認する。ObjectID の処理開始後は、その Snapshot に基づいて完了まで進める。
- 設定ファイルの各 disk は `cleanup_delete_allowed` / `cleanup_preserve_allowed` を任意指定でき、省略時は両方 false とする。実行中の変更は Management API を通す。外部コマンドから API を呼ぶ IPC / control socket は Management の公開境界として実装し、CLI が下位レイヤーや内部 map を直接操作しない。
- Object の論理削除状態と削除待ちキューは Orchestration が所有する。削除待ちキューは Deleted Header から再構築可能なメモリ上の候補索引であり、永続化したり Management / Distribution に複製したりしない。Management はドライブ単位の許可情報を Orchestration に渡す。Orchestration は許可ドライブと Header / Namespace の状態から、削除可能な ObjectID をすべて特定し、一括削除要求として Distribution に渡す。Management は ObjectID や StripeID を選択せず、個別 Object の削除をスケジュールしない。

### Orchestration

- Header の定義・生成・解析・シリアライズ・更新、論理 Object と Namespace、ファイル offset と StripeID / stripe 内 offset の変換、配置方針を担当する。
- 通常のファイルシステム操作における論理状態の所有者である。Namespace、Header、Object の論理削除状態、open handle、および Deleted Header から再構築する一時的な削除待ちキューを管理する。削除待ちキューは Orchestration が所有し、Management はドライブ単位の許可情報を渡す。Orchestration は Header / Namespace と許可ドライブを照合して削除可能な ObjectID を一括選択し、Distribution に一括削除を依頼する。
- Management が提供する共通実行ロック機構の制御下で呼び出される。Orchestration 自身はレイヤー横断のロック管理・ロック取得を行わない。Namespace の内部 map や Runtime 状態を外部へ直接公開しない。
- 初期化では Header の読み込み・解析・検証、Namespace 構築、HardLink 解決、整合性検査を行う。Deleted Header を持つ ObjectID を一時的な削除待ちキューへ登録し、不整合保護対象はキューから除外する。削除待ちキューは Orchestration 内部に保持し、削除実行時に許可ドライブと Header / Namespace の状態から削除可能な ObjectID を一括選択する。
- 論理削除操作では Header の Deleted 状態、Namespace の変更、および削除待ちキューへの登録を担当する。論理削除 Object の物理削除では、Orchestration が Management から受け取った許可ドライブ情報と自身の Header / Namespace 状態を照合し、削除可能な ObjectID をすべて選択して Distribution へ一括要求する。Distribution の ObjectID 単位の削除が完了した後、Orchestration は対象 Object の Runtime 状態と削除待ちキューを確定する。
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
- Cleanup の Version 分類は Object 全体の一括処理ではなく、ObjectID + StripeID ごとに、その Stripe の最新有効 Version が確定した時点で行う。起動時は全物理チャンクの列挙・Version 集約・復元可能性判定を先に完了させ、各 Stripe の最新有効 Version を確定した後、Stripe ごとに同じ分類処理を実行する。起動時に全 Stripe の分類を一度に行う専用ロジックを設けない。
- Stripe の分類処理は初期化時と Foreground Write 成功時で共通化する。共通処理は対象 Stripe の最新有効 Version と全 Version 情報から分類を導出し、有効 Version より古い Version を OldVersion、有効 Version より新しい Version を AbnormalVersion とする。最新有効 Version を確定できない Stripe は分類を保留し、削除候補を生成しない。
- Write 時の分類は、新 Version の全チャンク書き込みが成功し、Distribution の最新有効 Version（objects）と Version 情報（versions）を更新した後に行う。失敗した部分書き込みを、その場で正常系の旧 Version として分類しない。再起動時の復元可能性判定で有効 Version が確定した場合は、起動時の共通分類処理で改めて分類する。
- 共通分類処理は Stripe 内の Version 全体から分類結果を導出する。初期化時は既存 Version 全体が初回の対象となり、Write 時は更新後の全体状態から新たに発生した候補を追加する。候補キューへの登録は冪等とし、同一候補の重複登録で処理が重複しないようにする。
- 分類された Version の物理チャンクは IOIndex（Drive）ごとに分割し、Cleanup 候補キューへ登録する。候補の識別単位は IOIndex + ObjectID + StripeID + Method とし、キュー項目は物理ファイル一覧を固定的に保持するのではなく、処理時に対象 Stripe の最新状態から対象ファイルを再確定するための識別情報とする。Method は少なくとも DeleteOldVersion と PreserveAbnormal を区別する。
- DeleteOldVersion は許可された Drive 上で物理削除する。PreserveAbnormal は通常の削除対象にせず、同じ Drive 内の保全領域へ元の物理ファイル名を維持して Rename する。保全領域は通常の Distribution スキャン対象から除外する。
- Distribution は IOIndex + ObjectID + StripeID + Method を指定して一件の Cleanup 候補を処理する API を提供する。DeleteOldVersion は Delete、PreserveAbnormal は Preserve（同一物理ファイルシステム内の Rename）で処理し、対象チャンクのヘッダーや内容を Cleanup のために再解析しない。対象は Stripe の分類情報と物理 FileKey から特定する。
- 保全領域名は `pkg/shared/config.PreservationDirectory` を唯一の正とし、初期値は `.flexraid-preserved` とする。各 Drive の物理 mountpoint 直下に固定し、元の物理ファイル名を維持する。16進数だけの名前にはしない。通常の `ListChunks` / `ScanHeaders` は保全領域を再帰探索しない。
- 旧 Version / 異常 Version など、既に無効と確定した Stripe 内の Version 単位 Cleanup では Stripe 0 を最後にする順序制約を設けない。Stripe 0 を最後にする制約は、Header を含む論理削除 Object 全体の物理削除に限る。
- 分類・候補生成は map Mutex で共有状態を保護して行い、候補登録後に物理 I/O を行わずロックを解放する。Cleanup ワーカーは Distribution 内の自律ループとして動作し、実行開始ごとに Drive 単位の許可情報を読み取る。map Mutex の短時間の保護下で対象 FileKey を確定し、解放後に Delete / Rename を実行する。Cleanup のために Stripe Mutex を取得しない。キュー Mutex を保持したまま map Mutex 取得や物理 I/O を行わない。
- Mirror は有効な複製チャンクが1つ以上あれば復元可能とする。XOR 1 パリティは、全データチャンクがあればパリティチャンクが欠落していても復元可能とし、データチャンクが1つだけ欠落している場合はパリティチャンクを使って復元する。データチャンクが2つ以上欠落している場合は復元不能とする。
- 読み込み時は選択済み Version の各チャンクを再読込し、物理読み取りエラーや不正なチャンクを欠落として扱って復元を試みる。復元できなければデータ破損エラーを返す。欠落チャンクを復元できた場合は、呼び出し元から修復配置が与えられていれば同じ Version の欠落チャンクを修復する。修復は新しい Version のコミットではない。
- 復旧はチャンクの欠落または物理読み取りエラーにより取得できない場合を対象とする。読み取れたデータ同士の内容比較や、全データチャンクが読めた場合のパリティ不一致だけを理由にデータを訂正しない。チェックサム等によるサイレント破損・改ざん検出は保証しない。
- Distribution は StripeID ごとの状態・物理チャンク候補を所有する。Foreground の Read / Write / Recovery は同一 ObjectID + StripeID の Stripe ロックで保護する。自律 Cleanup は Stripe ロックを取得せず、最新有効 Version 以外の不変 FileKey の物理削除・退避を Foreground の有効チャンク操作から独立して行う。
- Distribution の共有 map（objects / versions / orphans / stripeLocks）へのアクセスは map Mutex で保護する。Stripe ロックの取得順序は必ず map Mutex → 対象 Stripe Mutex とする。Stripe Mutex の取得待ちで map Mutex を保持し続けてはならない。Stripe Mutex は map Mutex 保持中に TryLock し、取得できなければ map Mutex を解放して再試行する。これにより、Stripe ロック保持中の map 更新と、同じ Stripe を待つ処理のデッドロックを避ける。
- Foreground の Stripe 操作（Read / Write / Recovery / Delete / Sync / Stripe 情報取得）は、最新の選択済み recoverable Version をキャッシュしてから実行する。キャッシュの確認・読み込み・map 反映は map Mutex を取得してから対象 Stripe Mutex を取得し、キャッシュ確保と map 反映が完了するまで両方を保持する。その後 map Mutex だけを解放し、Foreground 操作は Stripe Mutex を操作完了まで保持する。異なる Stripe の操作は並行可能とする。
- Stripe のキャッシュは Foreground Read が物理 I/O を繰り返さずに済み、Background Cleanup が旧 Version / Chunk を物理削除した後も最新データを参照できるためのものとする。キャッシュを安全に確保できない場合、Foreground の更新・削除を開始しない。復元可能な欠損チャンクは既存の復旧契約に従って扱い、最新 Version 自体を復元不能と判断した場合は処理を中止する。
- Background Cleanup は Foreground の Stripe Mutex を使用しない。map Mutex の短時間の保護下で無効 FileKey を確定した後、map Mutex を解放して物理削除・退避する。削除後の map / queue 反映はそれぞれの Mutex で保護する。無効対象キューは専用 Mutex で保護し、キュー Mutex を保持したまま map Mutex 取得や物理 I/O を行わない。

- Cleanup には、Distribution が自律実行する不要 Version 等の物理 Cleanup と、Orchestration が対象 ObjectID を選択する論理削除 Object の物理 Cleanup の2種類がある。候補の所有者・実行制御・削除順序を混同しない。不要 Version 等は Distribution のバックグラウンドループが Stripe 単位で選択・処理する。論理削除 Object は Orchestration が削除可能な ObjectID を一括選択し、Distribution に ObjectID 単位で削除を依頼する。Management は両方に Drive 単位の許可情報を与えるだけで、ObjectID / StripeID / Chunk の選択や個別削除の dispatch を行わない。
- 論理削除 Object の Cleanup では、Distribution は ObjectID 単位の一括削除 API を提供する。Orchestration は許可ドライブ情報を受け取り、削除可能な ObjectID をすべて特定して一括要求する。Distribution は各 ObjectID について、まずその Object の無効 Version に属する物理データを削除し、続いて有効 Version のチャンクを後方（大きい StripeID / ChunkIndex）から順に削除する。これにより Stripe 0 は最後に残る。ObjectID の処理を開始した後は、Drive の許可条件が変更されても当該 ObjectID の削除を完了する。許可条件の再評価は次の ObjectID の選択・開始時に行う。処理に失敗して ObjectID の削除を完了できない場合は、その ObjectID を未完了として残し、後続 ObjectID の処理へ進まない。
- 初期パリティ方式は XOR 1 パリティであり、1 データチャンクの欠損まで復元できる。内容改ざん・サイレント破損の検出を保証しない。
- **旧 Version 等の自律 Cleanup** は Distribution が実行主体となる。Management は Distribution のバックグラウンドループを起動・停止し、各 Drive に対する Cleanup 許可情報を更新するだけとする。Cleanup の候補・Stripe / Object / Chunk・Method を指定する API や、個別 Cleanup を Management の Scheduler / MaintenanceExecutor から実行する経路を設けない。許可情報は `CleanupPermissionStore` の共有 Snapshot として参照する。不要 Version の Delete は `DeleteAllowed`、異常データの保全領域への Rename は `PreserveAllowed` を使用し、各物理ファイルの処理開始時に最新 Snapshot を確認する。
- Cleanup の候補選定・保護判定・実行順序・内部並列度は Distribution のバックグラウンドループが所有する。Management は候補一覧や Version / Chunk の内部状態を複製しない。Distribution は Drive ごとの許可レベルを参照し、許可された Drive 上の無効な物理 Chunk を順次削除する。Cleanup は Foreground の論理操作とは独立して実行し、Cleanup のための Object 単位・Stripe 単位ロックや Management 共通実行ロックを取得しない。物理ファイルは不変の Version として扱い、Foreground が参照・更新する最新有効 Version のファイルを候補に含めないこと、候補確定後に新しい Version が追加されても削除対象 FileKey が最新有効 Versionへ昇格しないことを保証する。物理 I/O 中に Distribution の共有 map ロックを保持せず、削除完了後の管理情報更新は短時間の map ロックで行う。
- Distribution は保護対象を Cleanup 候補から除外する。最新 Version に欠損が確認されている Stripe は、その Stripe の Cleanup 全体を保留し、通常の許可レベルだけでは実行しない。強制的な保護解除は未実装とし、保留対象を誤って削除しない。
- Cleanup は物理 FileKey が不変であることを利用し、Foreground が使用する最新有効 Version のファイルを削除対象から除外することで競合を防ぐ。Foreground Read / Write と Cleanup のために Stripe キャッシュの切り替えや Stripe ロックを共有しない。Cleanup は候補を値として確定してから物理 Delete / Rename を実行し、Foreground の処理を待たせない。
- Cleanup は Stripe ごとに自律的に進める。Stripe 内の無効 Chunk は、それが配置された Drive の許可レベルに従って削除できる。複数 Stripe の同時実行は Distribution 内部の並列化方針であり、Management の指示単位や Atomic の変更を意味しない。
- 自律 Cleanup の候補キューは Distribution が所有し、Foreground の Stripe 状態更新と Cleanup ワーカーの双方から更新される共有メモリである。候補キュー専用の Mutex で、候補の追加・削除・取得・更新をすべて保護する。キュー Mutex を保持したまま物理 I/O や Stripe の Cleanup 処理を実行しない。候補はキューから値として取り出し、ロック解放後に処理する。処理結果の反映・再登録もキュー Mutex の保護下で行う。
- 候補キューの Mutex は Stripe のデータ状態を保護するロックとは別物である。キュー Mutex を Stripe Atomic の代替にせず、Stripe の状態や物理データをキュー内の候補情報だけで判断しない。候補を取得した後、実行時点の Distribution 状態に基づいて削除対象を確定する。
- Queue Mutex の保持中に他の Distribution 状態ロックを取得する設計、または逆順で取得する設計を混在させない。ロック順序を固定し、キュー操作と Stripe 処理の間でデッドロックを発生させない。
- 自律 Cleanup は旧 Version、不正・復元不能 Version、未完了チャンク、孤立チャンク等の Distribution 所有の物理データを対象とする。最新の復元可能 Version は削除対象にしない。最新 Version の欠損がある Stripe は上記のとおり保留する。Stripe 0 を特別扱いする削除順序制約は設けない。Stripe 0 を最後にする制約は、論理削除 Object の全 Stripe 削除にのみ適用する。
- 配置ポリシーは決定せず、Orchestration から渡された配置方針を実際のチャンク配置に反映する。
- Distribution は Stripe / Chunk の読み書き、Version 選択、復旧、欠損・破損判定、Cleanup など、Distribution 自身の公開操作と内部処理で発生した事象を Distribution の統計として保持・集計する。物理ファイル単位の I/O 成否・エラー回数・物理ドライブ別 I/O 統計は I/O の所有物であり、Distribution に渡して再集計させない。Distribution の統計取得 API は Distribution 自身の統計だけを snapshot として返す。
- Distribution の `GetMetrics` は Cleanup 候補を Method（DeleteOldVersion / PreserveAbnormal）と IOIndex ごとに集計し、候補件数・物理ファイル長合計・削除による解放見込みサイズを返す。最新有効 Version を確定できない Stripe は候補に含めない。Cleanup 後の物理空き容量は Drive ごとの現在 AvailableBytes に削除候補のファイル長を加算した概算とし、その Drive 別見込み値を既定 Mirror / Parity 配置計算へ渡して論理空き容量を推定する。PreserveAbnormal は削除ではないため解放見込みに含めない。ファイル長はファイルシステムの実割当ブロック数を保証しない。

### I/O

- `io/` は物理ドライブ操作の唯一の公開窓口であり、物理ドライブ単位の実体を所有する。WWN・物理ファイルシステムの mountpoint・FlexRAID の storage root を対応付ける。物理ドライブへのアクセスは、読み書き・列挙・同期・削除・保全退避を含め、必ず I/O の公開 API を通す。
- I/O は raw な物理ファイル操作と、OS・実行環境に依存するデバイス機能を同じ公開パッケージで提供する。独立した Platform パッケージや、raw I/O 専用の内部パッケージは設けない。OS 差異は `io/` 内の非公開実装・OS 別ファイルで吸収し、上位層には共通 API と共通の結果型を提供する。
- 物理ファイル操作 API は `Read` / `Write` / `Delete` / `ScanHeaders` / `ListChunks` / `Preserve` / `Sync` などをドライブ単位で公開し、物理ファイルの内容を Header / Chunk / Stripe として解釈しない。Header 解析、配置方針、パリティ計算、復旧判断は Distribution / Orchestration の責務とする。
- I/O はファイルシステム容量、物理ドライブ識別・対応付け、デバイス能力・状態の取得、SpinUp / SpinDown 要求なども提供する。OS 固有 API は I/O パッケージ内に閉じ込める。
- デバイス能力・監視項目の対応可否は Initialization 時に一度プローブして確定する。SSD / 回転媒体の種別、スピンドルの有無、SMART 対応、SMART 全体ヘルス判定、温度取得可否、スピン状態・電源状態など、実際に判別できる情報だけを保持する。取得できない情報を推測で補完しない。SMART はデバイス自身の全体ヘルス判定と温度だけを対象とし、SMART 属性一覧・閾値・エラーログは収集しない。
- 監視値は取得特性に応じて扱う。要求時に低コストかつ非侵襲的に取得できる値はその都度取得する。ドライブが稼働中でなければ取得できない値は、通常の物理 I/O によりドライブが稼働した機会に取得する。I/O は物理 I/O 活動を把握し、前回取得から 1 分以上経過した対象値を稼働中に更新する監視ループを管理する。監視のために電源状態を先に問い合わせて稼働可否を判定したり、監視だけを目的にスリープ中のドライブへアクセスして起動させたりしない。
- 監視値には取得元・最終取得時刻・取得状態を付け、未対応・取得不能・未取得を区別する。スピン状態や電源状態を取得できない場合も推測しない。
- 物理ファイル操作の成功・失敗・所要時間、読み取りエラー発生回数、FileKey のエラーユニーク数、直近 1,000 処理はドライブ別・操作別に I/O が集計する。I/O の監視統計を取得する公開 API は `GetMetrics` 一つとし、独立した snapshot を返す。GetMetrics 自身は物理操作カウンターに含めない。
- I/O は物理操作・デバイス状態取得・デバイス制御の機構を提供する。Cleanup の候補選定・許可判断は Distribution / Management の責務とし、I/O はそれらのポリシーを持たない。SpinDown など Cleanup 以外の電源運用ポリシーは Management が担当する。

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
Management -> I/O
Orchestration -> Distribution
Distribution -> I/O
 
```

- Management は Orchestration と Distribution の双方を統括するが、Orchestration と Distribution の親子関係は維持する。通常のファイルシステム操作は Management -> Orchestration -> Distribution -> I/O -> io の経路を通る。
- I/O は物理ドライブ操作の唯一の公開窓口であり、Distribution と Management は I/O に依存する。内部 driveio は I/O の実装詳細であり、他レイヤーから見えない。
- Distribution は Platform から OS 固有のファイルシステム容量・デバイス情報を取得できる。Management は I/O を通じてドライブ状態の観測・制御を行う。I/O はそれらの判断ポリシーを持たない。
- ファイルシステム容量の取得を含む OS 固有処理は I/O に集約する。物理ファイル操作も I/O の公開 API に統一し、その raw 実装だけを `io/` に隔離する。
- Management から Distribution への直接呼び出しは、管理タスク・監視など、論理ファイル操作を必要としない責務に限る。通常のファイルデータ操作を Orchestration 経由から迂回させない。
- 下位層は上位層の型・状態・実装を参照しない。下位層から上位層への callback で依存グラフに逆向きの辺を作らない。結果・統計は下位層の公開 API を通じて上位層が取得する。
- 依存関係は DAG とし、循環依存を作らない。

### 現行実装からの移行

- Management は今後導入するレイヤーであり、現時点の実装は FUSE が Orchestration を直接呼び出し、Orchestration が自身の mutex で操作を排他している。この状態は移行前の実装であり、目標アーキテクチャを変更する理由にはしない。
- Management 導入時は FUSE の接続先を Management に変更し、Orchestration のグローバル mutex に依存する契約を Management 所有の共通実行ロック機構へ移行する。Object 単位などの対象別ロックへ分割しない。移行中に Orchestration のロックを単に削除して競合保護を失わせてはならない。
- Distribution の Stripe ロックは Distribution 自身が所有する。Management / Orchestration が Distribution の内部ロックを直接操作しない。

## 6. 制約値とファイルシステム属性

- 共有制約値は `pkg/shared/config` を唯一の正とする。現在の主要な制約には、最大ファイルサイズ 16 TiB、最大名前長 255 bytes、BlockSize 4 KiB、Header 予約領域 1 KiB がある。
- 単位を混同しない。I/O 容量は bytes、StatFs のブロック数は BlockSize 単位とする。
- StatFs は Orchestration が組み立て、FUSE は Linux statfs response へ写像する。物理総容量・空き容量と、冗長化を考慮した論理配置可能容量を区別する。
- FUSE の NLink / inode identity / mode / ownership / timestamps / size は Orchestration の論理属性と一貫させる。

## 7. 排他制御

### Management の共通実行ロック

- Management の実行ロックは単一の共通機構とする。Foreground / Background / Exclusive / Initialization / Stop のすべてが同じ機構を使用し、Management の実行単位を複数同時に実行しない。各単位は共通ロックを取得して直列実行し、完了後に解放する。
- Management では ObjectID、StripeID、Directory、File などの対象をキーにしたロックを設けない。複数 Object のロック集合や、Object ロックとは別の Maintenance ロックも設けない。
- ロックの取得・解放の粒度は、処理を中断しても整合性を保ち、再実行または再構築できる最小の実行単位とする。実行単位の開始前に共通ロックを取得し、その単位の処理結果と状態を確定してから解放する。
- ロックを保持したまま次の実行単位へ進まない。単位の境界で実行権を Scheduler に返し、待機中の Foreground / Exclusive / Stop 等を優先順位に従って調停する。
- ここでいう共通ロックは Management の実行単位を調停するための一つのロック機構であり、Object や Stripe のデータ構造をロック対象として列挙する仕組みではない。並行実行の可否・優先順位は Lifecycle / Scheduler の実行権ルールで決め、別のロック体系を追加して実現しない。
- Admission 登録と Lifecycle 遷移は原子的に調停する。状態確認だけを先に行ってから後で受付登録する構造にせず、drain 開始後に新規 Foreground が紛れ込まないようにする。
- ロック取得を待っている間に Lifecycle が変化した場合は、実行開始前に受付可否を再確認する。拒否された要求は下位レイヤーを呼び出さず、取得済みの実行権を解放する。
- Exclusive / Stop は新規 Foreground と Background unit の開始を止め、実行中の安全な単位が完了して共通ロックが解放されるのを待つ。複数 Exclusive はキュー順に実行し、Stop は最優先とする。
- Initialization も同じ共通ロック機構と安全単位を使用する。初期化中の Stop は現在の単位の完了境界で反映し、通常の Exclusive は初期化完了後に実行する。
- Release / Releasedir / Forget / Interrupt 等の kernel bookkeeping と handle teardown は通常操作の Lifecycle admission から除外し、停止・Failed 状態でも実行可能にする。Flush は通常操作であり、Release の代替として扱わない。
- Orchestration は Management の共通実行ロック機構の制御下で呼び出される。Orchestration 自身は Management のロック取得・解放や Lifecycle admission を行わない。
- FUSE callback はアダプターであり、Management の実行ロックを直接取得しない。複数の Management API を FUSE 側で組み合わせても原子性は得られない。不可分な操作は Management の単一 API とする。

### Distribution の内部 Stripe ロック

- Distribution は Management とは独立して、Distribution 内部の同時実行制約を管理する。Management の実行単位・共通ロックとは概念も粒度も異なり、相互に統合・共有しない。自律 Cleanup は Management の WorkItem / MaintenanceExecutor を待たず、Management が設定した Drive 単位の許可レベルに従って Distribution 内部で進行する。
- Distribution は同一 ObjectID + StripeID を Foreground Read / Write / Recovery の最小排他単位とする Stripe ロックを所有する。自律 Cleanup はそのロックを使用せず、Foreground と独立して動作する。Management の実行ロックとは独立しており、Management の WorkItem 境界を Distribution の Atomic に流用しない。
- 自律 Cleanup の許可は Drive 単位、物理削除対象は Chunk / FileKey 単位で選択する。Cleanup 対象は最新有効 Version 以外の不変 FileKey に限るため、Foreground の有効データのキャッシュ化・Read / Write・Stripe ロックに関与しない。
- 最新 Version の欠損が確認された Stripe と保護対象は Cleanup しない。最新 Version が復元可能であることを含め、削除候補の安全性を Distribution が判断する。
- 削除候補は実行時に map Mutex の保護下で最新有効 Version と照合し、無効な FileKey であることを再検証する。物理 I/O はすべての Distribution 状態ロックを解放してから実行する。
- 論理削除 Object の削除では Orchestration が削除可能な ObjectID を選び、Distribution が ObjectID 単位で物理削除する。Distribution は ObjectID の処理中に Management の実行ロックや Foreground の Stripe ロックを取得しない。ObjectID の削除完了後に Orchestration が論理 Object のメモリ状態を確定する。
- Distribution の Stripe ロック取得中に Management の実行ロックを要求する逆方向の依存を作らない。Management は Distribution の内部ロックを直接操作しない。
- I/O は物理ファイル操作を実行する。上位層の Object / Stripe の意味に基づくロックを持たず、必要性が実証された物理ファイル単位の排他だけを内部実装として持つ。

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

## 11. 物理ドライブ識別

物理ドライブの識別・対応付けに関する規約を定める。管理コマンドとプロセス構成は次節を正とする。

- FlexRAID のストレージデバイスは WWN によって識別する。
- Management が起動時に読み込む設定に、各ストレージのマウントポイントと物理ドライブ WWN を一組として記述する。WWN はマウントポイントに対応する物理ドライブを明示するものであり、設定から省略したり実行時の自動推測で補完したりしない。
- デバイス交換時には、交換先デバイスの識別子をコマンド引数として明示的に指定し、対象 I/O ルートとの対応付けを更新できるようにする。交換時に新しい識別子を指定できることを必須要件とする。
- 管理ユーティリティでは、ホストから認識できる対象デバイスと WWN を列挙し、利用者が交換先デバイスを選択できるようにする。モデル名・容量などは候補を区別する補助情報として表示する。
- 一覧からの選択と同等に、コマンドから識別子を直接指定する操作も可能にする。対話 UI の利用を必須にしない。
- 指定された識別子が見つからない場合や、一意に解決できない場合は、誤ったデバイスを対象にせず明確に失敗させる。既存の対応付けを暗黙に別デバイスへ切り替えない。
- この用途で必要なのは物理デバイスの識別子であり、Filesystem UUID などファイルシステムの識別情報を代用しない。
- 物理ドライブ UUID は WWN（World Wide Name）とする。Linux では `/dev/disk/by-id/wwn-*` 等で公開される WWN を基準にする。取得できないドライブは識別不能として扱い、シリアル番号など別形式への暗黙のフォールバックは行わない。WWN が重複するなど一意に解決できない場合も操作を拒否する。
- マウントポイントから物理デバイスまでの自動トポロジー解決は、このユーティリティの必須要件に含めない。必要なデバイス識別情報は列挙結果から利用者が選択し、設定へ明示的に反映する方針とする。
- 設定ファイルの具体的なフォーマット、設定更新の原子性、実行中プロセスへの反映タイミング、交換前後の安全確認、デバイスが未接続の場合の扱いは、該当機能の実装時に定義する。

## 12. Controller / Worker と外部管理 API

この節は、外部管理インターフェースとプロセス構成の確定した設計契約を定める。CLI コマンドの個別引数や RPC メッセージの詳細は、実装時に API 定義と合わせて決定する。

### プロセス構成

- 外部コマンドの受付を担う Controller と、論理ドライブを実際に管理する Worker を分離する。
- Worker は論理ドライブごとに独立した OS プロセスとして起動する。各 Worker プロセスは一つの論理ドライブを担当し、そのドライブに対応する Management と下位レイヤーの実行状態を所有する。
- 複数の論理ドライブは、それぞれ独立した Worker プロセスとして並行稼働できる。一つの Worker の異常終了が、他の Worker や Controller のプロセス終了を引き起こしてはならない。
- Controller は外部コマンドを受け付け、対象 Worker へ操作を中継する。Controller 自身は論理ドライブの Management 実行状態を所有しない。
- Controller は常駐 RPC サーバーモードと、コマンドを一度実行して終了する単発モードを持つ。両モードは同じ RPC 契約を使用する。
- 常駐 Controller が終了・強制終了しても Worker は動作を継続する。Controller は Worker の親プロセス／監督プロセスではなく、Worker の生存期間を所有しない。
- Controller から Worker を起動する場合も、Worker の独立起動コマンドを起動する薄いラッパーとして扱う。Controller 終了時に起動した Worker を連動終了させない。Worker は Controller を介さず単独でも起動できる。
- Worker の停止は対象 Worker への明示的な停止要求で行う。Controller 自身の停止と Worker の停止を同一視しない。
- Controller 再起動後は、既存 Worker の接続先を再発見して再接続できる設計とする。Controller のメモリ上の登録状態だけを Worker の存在根拠にしない。
- プロセス分離はアプリケーションプロセス間の障害伝播を防ぐものであり、OS・カーネル・物理デバイスなど共有基盤の障害まで分離するものではない。

### ローカル RPC

- Controller と Worker、および外部コマンドと Controller のプロセス間通信には、Unix Domain Socket 上の gRPC を使用する。
- RPC のメッセージ形式には Protocol Buffers を使用する。JSON を独自に重ねた RPC や独自バイナリプロトコルを追加しない。
- RPC 契約は Protocol Buffers の service / message 定義を正とし、Go の RPC コードは定義から生成する。生成コードを手編集しない。
- Controller API と Worker API は別の service 契約として定義する。Controller は drive_name に対応する Worker を特定し、Worker API を呼び出す。
- 外部利用者向けの管理コマンドは Controller を入口とする。Worker の UDS は Controller から Worker への内部通信に使用し、通常の CLI 利用者に Worker の接続先を直接指定させない。
- 単発 Controller コマンドは、常駐 Controller が利用可能なら Controller の UDS へ接続し、常駐 Controller がない場合も必要な Worker を特定して操作できる構成とする。単発モードと常駐モードで操作の意味や RPC 契約を変えない。
- Socket の配置、権限、所有者、起動時の stale socket 処理、Worker の識別・再発見方法は実装時に定義する。PID ファイルの存在だけで Worker の稼働を断定しない。

### コマンド体系

以下は整備するコマンド領域の一覧であり、現時点で実装済みの CLI 契約を意味しない。引数・出力形式・終了コードは各コマンドの API 設計時に定義する。

- `system status`: Controller および接続可能な Worker 群の状態を確認する。
- `system start`: Controller の常駐 RPC サーバーを起動する。
- `system stop`: Controller の常駐 RPC サーバーだけを停止する。Worker は停止しない。
- `drive list`: 管理対象の論理ドライブを列挙する。
- `drive status <drive_name>`: 簡易ヘルスチェックを行い、論理ドライブ全体として問題があるかを短時間で確認する。全デバイスの詳細照会やストレージ全走査を前提としない。
- `drive status <drive_name> --detail`: 運用監視用の詳細状態を取得する。物理ドライブごとの生存・接続・電源状態、容量、I/O 統計、整合性に関する実行時統計を、全体集計と併せて返す。ドライブ別の内訳を失わず、合計値だけに集約しない。
- `drive inspect <drive_name>`: 監査用の完全な情報を取得する。構成、論理 Namespace、Stripe / Version / Chunk の状態、物理ドライブごとの情報、Cleanup 対象の種類別件数・サイズ、現在の残論理空き容量、Cleanup 実行後に見込まれる残論理空き容量、履歴・統計など監査に必要な情報を網羅する。必要なストレージ走査を行う場合があるため、軽量なヘルスチェックや定期監視用途とは区別する。
- `drive start <drive_name>`: 指定論理ドライブの Worker を独立プロセスとして起動する。
- `drive stop <drive_name>`: 指定 Worker に停止を要求する。
- `cleanup status <drive_name>`: Cleanup の実行状態・進捗を取得する。Cleanup Schedule の情報は drive status --detail および drive inspect に含め、スケジュール専用 CLI コマンドは設けない。
- `cleanup policy show <drive_name>`: Cleanup の実行ポリシーを取得する。
- `cleanup policy set <drive_name> ...`: Cleanup の実行ポリシーを変更する。
- `cleanup run <drive_name>`: 指定論理ドライブで Cleanup を手動実行する。
- `config show`: Controller が扱う設定を表示する。
- `config validate`: 設定の妥当性を検証する。
- `config reload`: 設定を再読み込みする。

コマンド名と実行ファイル名は次のとおりとする。

- Controller の実行ファイル名は `frcon` とする。常駐 RPC サーバーの起動は `frcon serve`、それ以外の管理操作は `frcon` の単発コマンドとして提供する。
- Worker の実行ファイル名は `flexraid` とする。論理ドライブごとに一つの `flexraid` プロセスを起動し、Controller を介さない独立起動も可能にする。
- `frcon` と `flexraid` はそれぞれ正式な実行ファイル名とし、長い別名やシンボリックリンクによるエイリアスを前提にしない。
- Go のコマンド配置は `cmd/frcon` と `cmd/flexraid` を基本とし、生成される実行ファイル名も各ディレクトリ名に合わせる。

上記の階層・コマンド名は初期の整備対象であり、個別引数・出力形式・終了コードは実装時に既存設定・Management API と整合させて確定する。

### レイヤー別メトリクス API と所有権

- Management / Orchestration / Distribution / I/O の各レイヤーは、監視統計を取得する公開関数を一つだけ提供し、関数名は `GetMetrics` とする。Request / Response 型は各レイヤー固有とし、取得結果は内部状態を共有しない整合した snapshot とする。個別統計ごとの公開 getter を増やさず、追加項目は同じ `GetMetrics` Response に含める。
- 各レイヤーの統計は、その事象が発生したレイヤー自身が保持・集計する。下位レイヤーの物理ファイル統計やエラー履歴を上位レイヤーへ渡して再集計させない。上位レイヤーの操作結果と下位レイヤーの操作結果は、それぞれ独立した操作結果として記録する。
- Management は各レイヤーの `GetMetrics` を直接呼び出して snapshot を取得し、監視応答としてまとめる。Management は統計の所有者ではなく、他レイヤーの内部 map / slice を直接参照しない。物理 I/O 統計は I/O の GetMetrics から取得し、Management が設定上の drive_name と対応付ける。Distribution を経由して物理 I/O 統計を取得したり、Distribution に物理 I/O 統計を転送したりしない。
- I/O は物理ドライブ上で実行した物理ファイル操作の統計を所有する。Distribution は Stripe / Chunk の操作・復旧・欠損判定等、Distribution 内で発生した事象の統計を所有する。Orchestration は Object / 論理ファイル単位の操作・欠損判定等の統計を所有する。Management は Management 自身の公開操作の統計を所有する。
- 既存の個別統計取得 API は `GetMetrics` に統合する。特定レイヤーの統計を別のレイヤーの統計として再公開しない。

### 実行統計・欠損状態・エラー観測

- 各レイヤーは、自身が公開する各操作（コマンド/API）について成功数・失敗数を個別に集計する。操作名とレイヤーを集計キーに含め、異なる操作やレイヤーの結果を一つの総数へ潰さない。Management は自身の操作統計を所有し、下位レイヤーの統計は各所有レイヤーから snapshot として取得・集約する。
- I/O の物理操作統計・エラー統計は物理ドライブごと、かつ操作ごとに集計する。Distribution / Orchestration / Management はそれぞれ自身の操作ごとに成功数・失敗数を持つ。上位操作の失敗と下位操作の失敗は別々の事象として記録し、単純加算して同じ失敗として扱わない。
- 読み取りエラーは発生回数とユニーク対象数を分離する。物理層では物理ファイル（FileKey）単位で、ドライブごと・Platform 操作ごとにエラー対象のユニーク数と発生回数を記録する。同じ物理ファイルへの再試行はユニーク数を増やさず、発生回数だけを増やす。
- 物理ファイルの欠損状態は FileKey を識別子とするメモリ上の状態リストで保持し、ドライブごとに復旧可能な欠損ファイルと復旧不可能な欠損ファイルのユニーク数を分けて集計する。読み取りエラーが発生したことだけで欠損と断定せず、確認された欠損状態・復旧判定を別に保持する。
- 論理層では ObjectID 単位で、読み取りエラーのユニーク数・発生回数、復旧可能な欠損を持つ論理ファイル数、復旧不可能な欠損を持つ論理ファイル数を分けて集計する。論理ファイルの復旧可能性は Stripe / Chunk の物理状態から論理ファイルとして判定し、物理ファイルの件数を単純合算しない。 `Orchestration.GetMetrics` は論理操作別の成功・失敗数、累積所要時間、直近 1,000 件の成功・失敗・所要時間・エラー種別を返す。ObjectID ごとの読み取りエラー発生数・ユニーク数と直近 1,000 件の発生数・ユニーク数・エラー種別も返す。論理欠損対象は ObjectID のメモリ上の集合として保持し、起動時および `GetMetrics` 時に Distribution の詳細 Stripe / Version snapshot から再構築する。読み取りエラー履歴・累積カウンターは再起動後に復元しない。Context のキャンセル・期限切れはデータ読み取りエラー対象に含めない。
- 各レイヤー・各操作について、累積の成功数・失敗数・エラー発生回数に加え、直近 1,000 処理の成功数・失敗数・エラー発生回数を保持する。直近 1,000 処理は操作単位で独立した固定長履歴とし、I/O はさらに物理ドライブごとに分ける。処理が 1,000 件未満の場合は存在する全件を対象とし、分母となる実処理数も返す。
- 欠損対象リスト、ユニークエラー対象リスト、累積カウンター、直近 1,000 処理の履歴はメモリ上だけに保持する。再起動後の復元・永続化は行わず、起動時にストレージ状態から再構築できる欠損状態のみ再構築する。読み取りエラーの過去履歴やカウンターをストレージ走査から推測・復元しない。
- 必要な監視カウンターは外部の監視側が Status / Inspect 等の取得 API を定期的に呼び出して保存する。FlexRAID 自身は監視カウンターを永続化しない。詳細な個別事象の履歴が必要な場合はログを使用し、集計カウンターの代わりに無制限のイベント履歴をメモリ保持しない。
- ログには調査に必要な操作名、レイヤー、対象識別子（物理ファイル・ドライブ・Object 等、該当する範囲）、結果、エラー、および所要時間を記録する。ログは詳細履歴の参照手段であり、監視用カウンターはログを再解析して生成する設計にしない。
- カウンター・状態リスト・リングバッファは同時 I/O / 操作から安全に更新・snapshot 取得できるよう同期する。公開 API は内部 map / slice を共有せず、整合したコピーを返す。

### Status / Inspect の情報契約

- Management の公開監視 API は `GetStatus` / `GetDetailedStatus` / `Inspect` の3種類に分ける。これらはそれぞれ簡易ヘルスチェック、運用監視 snapshot、完全監査 snapshot であり、一つの API のフラグで用途を切り替えない。
- `GetStatus` は Lifecycle とメモリ上の累積異常カウンターを用いる低コストな要約とし、物理デバイス照会・容量照会・ストレージ全走査を行わない。
- `GetDetailedStatus` は Distribution と I/O の `GetMetrics` snapshot、および I/O のデバイス識別・容量情報を drive_name / WWN ごとに返す。I/O の Drive 順序は設定ドライブ順と一致させ、Distribution が設定されている場合は件数不一致を初期化時に拒否する。
- `Inspect` は詳細監視に加えて Distribution の物理 Object / Stripe / Version / Chunk / Orphan 情報と Orchestration の論理 Namespace / Object / 初期化警告を含める。詳細状態 API の単なる別名にしない。
- 物理情報の取得失敗は項目ごとに取得不能として返し、他ドライブの情報を捨てない。値を推測で埋めず、取得元・最終取得時刻を明示する。監視・監査のためだけに standby デバイスを SpinUp しない。 詳細状態は取得できた I/O の物理操作統計・物理容量のドライブ別内訳と全体集計、Cleanup worker 稼働状態、Drive ごとの Cleanup 許可設定、Orchestration が算出する現在の論理空き容量も含める。Cleanup 候補サイズと Cleanup 後の容量見積もりは Distribution の `GetMetrics` が返す。サイズは I/O の走査結果または書き込み時に取得した物理ファイル長であり、ファイルシステムの実割当ブロック数とは区別する。物理デバイス属性など未実装・未取得の情報はゼロや推測値で代用しない。

- `status` の通常表示は、Lifecycle、Worker の生存、Controller との接続、物理ドライブの基本的な到達可能性などから、論理ドライブ全体の正常性を簡潔に判定する。高コストな全ストレージ走査を実行しない。
- `status --detail` は運用監視用 snapshot とする。論理ドライブ全体の集計値に加え、各物理ドライブを識別できる独立した要素として返す。I/O・整合性・容量について、ドライブ別に観測可能な値は全体値とドライブ別値の両方を返す。
- `status --detail` の物理ドライブ情報には、WWN、デバイスパス、モデル、シリアル、接続方式、物理容量、ファイルシステム、mountpoint、Storage Root、生存・接続状態、電源状態、I/O エラー、最終アクセス時刻などを含める。起動時から変化しない情報は Ready 前に取得した snapshot を使用し、稼働中に変化する情報は次の更新規則に従う。I/O 回数・転送量・レイテンシ・I/O エラー・最終アクセス時刻などの実績値は、読み込み・書き込みなど実際の I/O のたびに、対象物理ドライブと全体集計の両方を更新する。物理デバイスの接続・電源状態など OS が提供する低コストな状態値は、status 照会のたびに再取得して snapshot を更新する。照会コストが高い値は個別の更新間隔を定め、その間はキャッシュを返す。更新間隔・最終取得時刻・取得元を明示し、古い値を最新値として扱わない。取得できない値は不明として表現し、推測で補完しない。
- 物理デバイス情報の取得は I/O が担当する。起動後に変化しない識別・構成情報（WWN、デバイスパス、モデル、シリアル、接続方式、物理容量など）は、Initialization 中、Lifecycle が Ready へ遷移する前に取得して snapshot として保持する。取得にデバイスの起動が必要な項目は、Initialization 中の通常の SpinUp 処理と連携して取得する。監視値の定期更新では別途 SpinUp せず、通常 I/O による稼働機会を利用する。OS の非侵襲的な情報源から取得できる項目は、不要な SpinUp を行わず取得する。起動時に取得できなかった値は推測で補完せず、取得不能または未取得として理由を保持する。稼働中の更新規則は情報の性質と取得コストに応じて分ける。I/O 回数・転送量・レイテンシ・I/O エラー・最終アクセス時刻など実際の操作で得られる実績値は、読み込み・書き込みなど I/O 経由の各物理操作の完了時に対象物理ドライブ別および全体集計を更新する。OS の sysfs 等から低コストかつ非侵襲的に取得できる値は要求時に取得する。ドライブが稼働中でなければ取得できない値は、通常の物理 I/O でドライブが稼働した機会に、前回取得から 1 分以上経過していれば I/O の監視ループが更新する。電源状態を先に問い合わせて稼働可否を判定する方式にはせず、監視だけを目的に Standby 中のデバイスへアクセスして SpinUp させない。取得元・最終取得時刻・更新間隔・取得状態を保持し、古い値を最新値として扱わない。
- 物理ドライブの生存状態は、Worker の生存状態や論理ドライブの Lifecycle と別に表現する。少なくとも OS からのデバイス認識状態と、最後に確認できた I/O 成否を区別し、単なるデバイスノードの存在だけで物理媒体の正常動作を断定しない。Standby 中など現在の応答性を確認できない場合は、最終確認時刻とともに Unknown / LastKnown として返す。物理デバイス単位の状態を全体の単一状態に潰さない。
- Cleanup 対象は Deprecated Versions / Abnormal Data / Deleted Logical Objects など対象種別ごとに件数とサイズを返す。物理ドライブ別に算出できる対象サイズもドライブごとに返す。
- 容量は物理容量・物理使用量・物理空き容量と、冗長化・配置制約を反映した論理使用量・現在の残論理空き容量を区別する。Cleanup による物理解放見込みと、Cleanup 後に見込まれる残論理空き容量も別項目とする。
- 整合性・I/O 統計は、全体集計に加えて物理ドライブ別の内訳を返す。Stripe の復旧可能性など複数ドライブにまたがる判定は論理ドライブ全体の値として扱い、ドライブ別の欠損・エラー状況と混同しない。
- `inspect` は監査用の完全な snapshot とする。構成・デバイス識別情報・Namespace / Object / Stripe / Version / Chunk の監査情報、Cleanup 候補の詳細とサイズ、容量見積もり、統計および取得時刻を含める。全走査が必要な情報は走査結果であることと実施時刻を明示し、未実施の値をゼロとして扱わない。
- Cleanup Schedule は `status --detail` の Cleanup 情報内に、Cleanup の状態・Policy などと同列の情報項目として含める。`inspect` にも監査情報として含める。独立した公開関数・RPC・CLI コマンドは設けず、Status / Inspect の各応答に含める。スケジュールの設定・変更・無効化機能は別途要件が確定するまで追加しない。
- Controller API / Worker API の RPC 名や Response 型は、上記の用途を混同しないよう Status と Inspect を別操作として定義する。
