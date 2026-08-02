using UnityEngine;

namespace TestPlayFixture
{
    public sealed class DeterministicProbe : MonoBehaviour
    {
        public int Value { get; private set; }

        private void Awake()
        {
            Value = DeterministicState.ExpectedValue;
        }
    }

    public static class DeterministicState
    {
        public const int ExpectedValue = 42;

        public static int Combine(int left, int right)
        {
            return left * 10 + right;
        }
    }
}
