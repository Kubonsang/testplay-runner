using System;
using System.Reflection;
using UnityEditor.TestTools.TestRunner.Api;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Compatibility shim for TestRunnerApi members that changed visibility or
    /// return type between supported Test Framework versions. Missing members
    /// are a correctness unknown, so callers reject warm execution.
    /// </summary>
    internal static class TestRunnerApiCompat
    {
        private const BindingFlags StaticAny = BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.Static;
        private const BindingFlags InstanceAny = BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.Instance;

        private static readonly MethodInfo AnyRunActiveMethod =
            FindMethod("IsAnyRunActive", StaticAny, Type.EmptyTypes) ??
            FindMethod("IsRunActive", StaticAny, Type.EmptyTypes);

        private static readonly MethodInfo RunActiveMethod =
            FindMethod("IsRunning", StaticAny, new[] { typeof(string) });

        private static readonly MethodInfo ExecuteMethod =
            FindMethod("Execute", InstanceAny, new[] { typeof(ExecutionSettings) });

        private static readonly MethodInfo CancelMethod =
            FindMethod("CancelTestRun", StaticAny, new[] { typeof(string) });

        public static bool TryGetAnyRunActive(out bool isActive)
        {
            isActive = true;
            if (AnyRunActiveMethod == null || AnyRunActiveMethod.ReturnType != typeof(bool))
                return false;

            try
            {
                isActive = (bool)AnyRunActiveMethod.Invoke(null, null);
                return true;
            }
            catch (Exception e)
            {
                UnityEngine.Debug.LogWarning($"[TestPlay.Bridge] active-run probe failed: {Unwrap(e).Message}");
                return false;
            }
        }

        public static string ExecuteOwned(TestRunnerApi api, ExecutionSettings settings)
        {
            if (api == null)
                throw new ArgumentNullException(nameof(api));
            if (settings == null)
                throw new ArgumentNullException(nameof(settings));
            if (ExecuteMethod == null || ExecuteMethod.ReturnType != typeof(string))
                throw new NotSupportedException("this Test Framework cannot return an owned test-run GUID");

            try
            {
                var guid = ExecuteMethod.Invoke(api, new object[] { settings }) as string;
                if (string.IsNullOrEmpty(guid))
                    throw new InvalidOperationException("TestRunnerApi.Execute returned an empty run GUID");
                return guid;
            }
            catch (TargetInvocationException e)
            {
                throw Unwrap(e);
            }
        }

        public static bool TryGetRunActive(string guid, out bool isActive)
        {
            isActive = true;
            if (string.IsNullOrEmpty(guid) || RunActiveMethod == null || RunActiveMethod.ReturnType != typeof(bool))
                return false;

            try
            {
                isActive = (bool)RunActiveMethod.Invoke(null, new object[] { guid });
                return true;
            }
            catch (Exception e)
            {
                UnityEngine.Debug.LogWarning($"[TestPlay.Bridge] owned-run probe failed for {guid}: {Unwrap(e).Message}");
                return false;
            }
        }

        public static bool TryCancel(string guid)
        {
            if (string.IsNullOrEmpty(guid) || CancelMethod == null || CancelMethod.ReturnType != typeof(bool))
                return false;

            try
            {
                return (bool)CancelMethod.Invoke(null, new object[] { guid });
            }
            catch (Exception e)
            {
                UnityEngine.Debug.LogWarning($"[TestPlay.Bridge] could not cancel run {guid}: {Unwrap(e).Message}");
                return false;
            }
        }

        private static MethodInfo FindMethod(string name, BindingFlags flags, Type[] parameterTypes)
        {
            return typeof(TestRunnerApi).GetMethod(name, flags, null, parameterTypes, null);
        }

        private static Exception Unwrap(Exception e)
        {
            return e is TargetInvocationException tie && tie.InnerException != null ? tie.InnerException : e;
        }
    }
}
