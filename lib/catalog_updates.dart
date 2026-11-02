import 'package:flutter/foundation.dart';

import 'models.dart';

class CatalogUpdates extends ChangeNotifier {
  final _items = <String, Drama>{};
  final _coverRetries = <String, int>{};
  Drama? latest;

  /// 剧集快照被手动同步过几次（0.8.20）。
  ///
  /// 首页只认"变没变"，所以用一个自增计数而不是时间戳：同一秒内连点两次
  /// 也能区分开。快照同步本身不产生 [Drama]，走 [publish] 那条路会让首页
  /// 误以为某部剧的资料更新了，所以单开一个通道。
  int _snapshotRevision = 0;

  int get snapshotRevision => _snapshotRevision;

  /// 站源管理页手动同步成功后调用：通知首页整库重读缓存。
  void markSnapshotSynced() {
    _snapshotRevision++;
    notifyListeners();
  }

  Drama current(Drama drama) =>
      _items[drama.id] == null ? drama : drama.merge(_items[drama.id]!);

  int coverRevision(String id) => _coverRetries[id] ?? 0;

  void publish(Drama drama, {bool retryCover = false}) {
    final previous = _items[drama.id];
    latest = previous?.merge(drama) ?? drama;
    _items[drama.id] = latest!;
    if (retryCover) _coverRetries[drama.id] = coverRevision(drama.id) + 1;
    notifyListeners();
  }
}
