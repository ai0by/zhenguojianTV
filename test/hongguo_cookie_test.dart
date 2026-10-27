import 'package:duanju_app/local_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<LocalStore> create() async {
    SharedPreferences.resetStatic();
    SharedPreferencesStorePlatform.instance =
        InMemorySharedPreferencesStore.withData(<String, Object>{});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  test('0.8.14：红果登录态能存住、能清除，首尾空白会被裁掉', () async {
    final store = await create();
    expect(store.hongguoCookie, isEmpty);

    await store.setHongguoCookie('  sessionid=abc; ttwid=xyz  ');
    expect(store.hongguoCookie, 'sessionid=abc; ttwid=xyz');

    await store.setHongguoCookie('');
    expect(store.hongguoCookie, isEmpty);
  });
}
